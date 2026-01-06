package caldav

import (
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/emersion/go-webdav/caldav"
	"github.com/emersion/go-ical"
	log "github.com/sirupsen/logrus"
	dauth "github.com/xinsnake/go-http-digest-auth-client"
)

type Client struct {
	client     *caldav.Client
	httpClient *http.Client
	logger     *log.Entry
	baseURL    string
}


func NewClient(caldavURL, username, password string) (*Client, error) {
	logger := log.WithFields(log.Fields{
		"module": "caldav",
		"url":    caldavURL,
	})
	
	logger.WithFields(log.Fields{
		"username": username,
		"has_password": password != "",
	}).Info("creating CalDAV client")

	httpClient := &http.Client{
		Timeout: 30 * time.Second,
	}
	
	if username != "" && password != "" {
		// Try Digest authentication first, then fall back to Basic
		digestTransport := dauth.NewTransport(username, password)
		httpClient.Transport = &AuthTransport{
			Username: username,
			Password: password,
			Digest:   &digestTransport,
		}
	}
	
	client, err := caldav.NewClient(httpClient, caldavURL)
	if err != nil {
		logger.WithError(err).Error("failed to create CalDAV client")
		return nil, err
	}

	// Extract base URL from the CalDAV URL
	parsedURL, err := url.Parse(caldavURL)
	if err != nil {
		logger.WithError(err).Error("failed to parse CalDAV URL")
		return nil, err
	}
	baseURL := fmt.Sprintf("%s://%s", parsedURL.Scheme, parsedURL.Host)

	return &Client{
		client:     client,
		httpClient: httpClient,
		logger:     logger,
		baseURL:    baseURL,
	}, nil
}

type AuthTransport struct {
	Username string
	Password string
	Digest   *dauth.DigestTransport
}

func (t *AuthTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	// Clone the request to preserve headers for both auth attempts
	req2 := req.Clone(req.Context())
	
	// First try with digest authentication
	resp, err := t.Digest.RoundTrip(req)
	if err == nil && resp.StatusCode != http.StatusUnauthorized {
		return resp, nil
	}
	if resp != nil {
		resp.Body.Close()
	}
	
	// If digest fails, try basic authentication with the cloned request
	req2.SetBasicAuth(t.Username, t.Password)
	return http.DefaultTransport.RoundTrip(req2)
}


func (c *Client) GetEvents(ctx context.Context, start, end time.Time, calendarNames []string) ([]caldav.CalendarObject, error) {
	l := c.logger.WithField("fn", "GetEvents")
	
	l.WithFields(log.Fields{
		"start": start.Format(time.RFC3339),
		"end":   end.Format(time.RFC3339),
		"calendar_names": calendarNames,
	}).Info("starting event retrieval")
	
	// Standard CalDAV discovery flow
	principal, err := c.client.FindCurrentUserPrincipal(ctx)
	if err != nil {
		l.WithError(err).Error("failed to find current user principal")
		return nil, err
	}
	l.WithField("principal", principal).Debug("found current user principal")
	
	homeSet, err := c.client.FindCalendarHomeSet(ctx, principal)
	if err != nil {
		l.WithError(err).Error("failed to find calendar home set")
		return nil, err
	}
	l.WithField("home_set", homeSet).Debug("found calendar home set")
	
	calendars, err := c.client.FindCalendars(ctx, homeSet)
	if err != nil {
		l.WithError(err).Error("failed to find calendars")
		return nil, err
	}
	
	return c.processCalendars(ctx, calendars, start, end, calendarNames, l)
}

func (c *Client) processCalendars(ctx context.Context, calendars []caldav.Calendar, start, end time.Time, calendarNames []string, l *log.Entry) ([]caldav.CalendarObject, error) {
	l.WithField("calendar_count", len(calendars)).Info("found calendars")
	
	// Log details about each calendar found
	for i, calendar := range calendars {
		l.WithFields(log.Fields{
			"calendar_index": i,
			"calendar_name": calendar.Name,
			"calendar_path": calendar.Path,
		}).Info("available calendar")
	}
	
	var allEvents []caldav.CalendarObject
	for _, calendar := range calendars {
		l.WithFields(log.Fields{
			"calendar_name": calendar.Name,
			"calendar_path": calendar.Path,
		}).Info("processing calendar")
		
		// Filter calendars if specific names are provided
		if len(calendarNames) > 0 {
			found := false
			for _, name := range calendarNames {
				if calendar.Name == name {
					found = true
					break
				}
			}
			if !found {
				l.WithField("calendar", calendar.Name).Debug("skipping calendar (not in filter)")
				continue
			}
		}
		
		l.WithField("calendar", calendar.Path).Debug("fetching events from calendar")
		
		query := &caldav.CalendarQuery{
			CompFilter: caldav.CompFilter{
				Name: "VCALENDAR",
				Comps: []caldav.CompFilter{{
					Name:  "VEVENT",
					Start: start.UTC(),
					End:   end.UTC(),
				}},
			},
		}

		l.WithFields(log.Fields{
			"calendar": calendar.Path,
			"query_start": start.Format(time.RFC3339),
			"query_end": end.Format(time.RFC3339),
		}).Debug("executing calendar query")

		events, err := c.queryCalendar(ctx, calendar.Path, query)
		if err != nil {
			l.WithError(err).WithField("calendar", calendar.Path).Error("failed to query calendar")
			continue
		}

		l.WithFields(log.Fields{
			"calendar": calendar.Path,
			"events_found": len(events),
		}).Info("calendar query result")

		// Log details about each event found
		for i, event := range events {
			var dataLength int
			if event.Data != nil {
				dataLength = len(event.Data.Children)
			}
			l.WithFields(log.Fields{
				"calendar": calendar.Path,
				"event_index": i,
				"event_path": event.Path,
				"event_components": dataLength,
			}).Debug("found event")
		}

		allEvents = append(allEvents, events...)
	}

	l.WithField("count", len(allEvents)).Info("retrieved events")
	return allEvents, nil
}


func (c *Client) queryCalendar(ctx context.Context, path string, query *caldav.CalendarQuery) ([]caldav.CalendarObject, error) {
	events, err := c.client.QueryCalendar(ctx, path, query)
	if err != nil {
		// Handle the depth header issue that some CalDAV servers require
		if strings.Contains(err.Error(), "Set Depth to 1") {
			c.logger.WithField("path", path).Debug("depth header issue detected, trying fallback approach")
			return c.queryCalendarWithDepth(ctx, path, query)
		}
		return nil, err
	}
	return events, nil
}

func (c *Client) queryCalendarWithDepth(ctx context.Context, path string, query *caldav.CalendarQuery) ([]caldav.CalendarObject, error) {
	// Try using the original client approach but with a different path construction
	c.logger.WithField("path", path).Debug("trying alternative client approach")
	
	// Create a new CalDAV client specifically for this calendar path
	fullCalendarURL := c.baseURL + path
	c.logger.WithField("full_url", fullCalendarURL).Debug("creating new CalDAV client")
	
	// Use the same HTTP client (with auth) but create a new CalDAV client
	newClient, err := caldav.NewClient(c.httpClient, fullCalendarURL)
	if err != nil {
		return nil, err
	}
	
	// Try querying at the root level (empty path)
	events, err := newClient.QueryCalendar(ctx, "", query)
	if err != nil {
		c.logger.WithError(err).Debug("root level query failed, trying different approach")
		
		// If that fails, try a simple GET request to see what's available
		return c.tryDirectGetRequest(ctx, path, query)
	}
	
	return events, nil
}

func (c *Client) tryDirectGetRequest(ctx context.Context, path string, query *caldav.CalendarQuery) ([]caldav.CalendarObject, error) {
	// Use PROPFIND to discover calendar objects
	req, err := http.NewRequestWithContext(ctx, "PROPFIND", c.baseURL+path, nil)
	if err != nil {
		return nil, err
	}
	
	// Set headers
	req.Header.Set("Depth", "1")
	req.Header.Set("Content-Type", "application/xml")
	
	// Build PROPFIND request body
	propfindBody := `<?xml version="1.0" encoding="UTF-8"?>
<D:propfind xmlns:D="DAV:" xmlns:C="urn:ietf:params:xml:ns:caldav">
  <D:prop>
    <D:resourcetype/>
    <D:getetag/>
    <D:getcontenttype/>
    <C:calendar-data/>
  </D:prop>
</D:propfind>`
	
	req.Body = io.NopCloser(strings.NewReader(propfindBody))
	req.ContentLength = int64(len(propfindBody))
	
	c.logger.WithFields(log.Fields{
		"method": req.Method,
		"url": req.URL.String(),
		"body": propfindBody,
	}).Debug("trying PROPFIND request")
	
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	
	if resp.StatusCode != http.StatusMultiStatus {
		respBody, _ := io.ReadAll(resp.Body)
		c.logger.WithFields(log.Fields{
			"status_code": resp.StatusCode,
			"response_body": string(respBody),
		}).Debug("PROPFIND request failed")
		return []caldav.CalendarObject{}, nil
	}
	
	// Parse the response
	return c.parseMultiStatusResponse(resp)
}

type MultiStatus struct {
	XMLName   xml.Name   `xml:"multistatus"`
	Responses []Response `xml:"response"`
}

type Response struct {
	Href     string   `xml:"href"`
	PropStat PropStat `xml:"propstat"`
}

type PropStat struct {
	Prop   Prop   `xml:"prop"`
	Status string `xml:"status"`
}

type Prop struct {
	CalendarData string `xml:"calendar-data"`
	ETag         string `xml:"getetag"`
}

func (c *Client) parseMultiStatusResponse(resp *http.Response) ([]caldav.CalendarObject, error) {
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	
	c.logger.WithField("response_body", string(body)).Debug("CalDAV response")
	
	var multiStatus MultiStatus
	if err := xml.Unmarshal(body, &multiStatus); err != nil {
		return nil, fmt.Errorf("failed to parse XML response: %w", err)
	}
	
	var events []caldav.CalendarObject
	for _, response := range multiStatus.Responses {
		if response.PropStat.Prop.CalendarData != "" {
			// Parse the calendar data using go-ical
			cal, err := ical.NewDecoder(strings.NewReader(response.PropStat.Prop.CalendarData)).Decode()
			if err != nil {
				c.logger.WithError(err).WithField("href", response.Href).Warn("failed to parse calendar data")
				continue
			}
			
			events = append(events, caldav.CalendarObject{
				Path: response.Href,
				Data: cal,
			})
		}
	}
	
	return events, nil
}

