package server

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/robertlestak/caldav-busy/internal/busy"
	"github.com/robertlestak/caldav-busy/internal/cache"
	"github.com/robertlestak/caldav-busy/internal/caldav"
	"github.com/robertlestak/caldav-busy/internal/config"
	log "github.com/sirupsen/logrus"
)

type Server struct {
	config    *config.Config
	clients   map[string]*caldav.Client
	generator *busy.BusyGenerator
	caches    map[string]*cache.Cache
	logger    *log.Entry
}

func NewServer(cfg *config.Config) (*Server, error) {
	clients := make(map[string]*caldav.Client)
	caches := make(map[string]*cache.Cache)

	// Create clients and caches for each CalDAV configuration
	for _, caldavCfg := range cfg.CalDAV {
		client, err := caldav.NewClient(caldavCfg.URL, caldavCfg.Username, caldavCfg.Password)
		if err != nil {
			return nil, fmt.Errorf("failed to create CalDAV client for '%s': %w", caldavCfg.Name, err)
		}
		clients[caldavCfg.Name] = client
		caches[caldavCfg.Name] = cache.NewCache(caldavCfg.RefreshInterval.Duration())
	}

	return &Server{
		config:    cfg,
		clients:   clients,
		generator: busy.NewGenerator(),
		caches:    caches,
		logger:    log.WithField("module", "server"),
	}, nil
}

func (s *Server) busyHandler(w http.ResponseWriter, r *http.Request) {
	l := s.logger.WithField("fn", "busyHandler")

	// Parse URL path
	// Expected formats:
	// /{name}/calendar.ics - combined calendar for all calendars under the named config
	// /{name}/{calendar}.ics - specific calendar
	pathParts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	if len(pathParts) != 2 {
		http.Error(w, "Invalid path format. Expected: /{name}/calendar.ics or /{name}/{calendar}.ics", http.StatusBadRequest)
		return
	}

	caldavName := pathParts[0]
	filename := pathParts[1]
	
	if !strings.HasSuffix(filename, ".ics") {
		http.Error(w, "Path must end with .ics", http.StatusBadRequest)
		return
	}

	calendarName := strings.TrimSuffix(filename, ".ics")
	if calendarName == "" {
		http.Error(w, "Calendar name is required", http.StatusBadRequest)
		return
	}

	// Get CalDAV configuration
	caldavCfg, err := s.config.GetCalDAVByName(caldavName)
	if err != nil {
		l.WithError(err).WithField("caldav_name", caldavName).Error("CalDAV configuration not found")
		http.Error(w, fmt.Sprintf("CalDAV configuration '%s' not found", caldavName), http.StatusNotFound)
		return
	}

	// Get client
	client, ok := s.clients[caldavName]
	if !ok {
		l.WithField("caldav_name", caldavName).Error("CalDAV client not found")
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	// Determine which calendars to query
	var calendarsToQuery []string
	var cacheKey string
	
	if calendarName == "calendar" {
		// Combined calendar - check if enabled
		if !caldavCfg.Combined {
			l.WithFields(log.Fields{
				"caldav_name":   caldavName,
				"calendar_name": calendarName,
			}).Error("Combined calendar not enabled for this configuration")
			http.Error(w, fmt.Sprintf("Combined calendar not enabled for configuration '%s'", caldavName), http.StatusNotFound)
			return
		}
		// Combined calendar - use all calendars
		calendarsToQuery = caldavCfg.Calendars
		cacheKey = caldavName + "_all"
	} else {
		// Check if the requested calendar exists in the configuration
		found := false
		for _, cal := range caldavCfg.Calendars {
			if cal == calendarName {
				found = true
				break
			}
		}
		if !found {
			l.WithFields(log.Fields{
				"caldav_name":    caldavName,
				"calendar_name":  calendarName,
				"available_cals": caldavCfg.Calendars,
			}).Error("Calendar not found in configuration")
			http.Error(w, fmt.Sprintf("Calendar '%s' not found in configuration '%s'", calendarName, caldavName), http.StatusNotFound)
			return
		}
		
		// Single calendar
		calendarsToQuery = []string{calendarName}
		cacheKey = caldavName + "_" + calendarName
	}

	// Get cache for this configuration
	cache, ok := s.caches[caldavName]
	if !ok {
		l.WithField("caldav_name", caldavName).Error("Cache not found")
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	// Check cache with specific key
	if data, valid := cache.GetWithKey(cacheKey); valid {
		l.WithFields(log.Fields{
			"caldav_name":    caldavName,
			"calendar_name":  calendarName,
			"cache_key":      cacheKey,
		}).Debug("serving cached busy data")
		w.Header().Set("Content-Type", "text/calendar")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(data))
		return
	}

	l.WithFields(log.Fields{
		"caldav_name":    caldavName,
		"calendar_name":  calendarName,
		"cache_key":      cacheKey,
	}).Info("cache miss, fetching fresh data")

	now := time.Now()
	start := now.AddDate(0, 0, -caldavCfg.TimeWindow.BackDays)
	end := now.AddDate(0, 0, caldavCfg.TimeWindow.ForwardDays)

	l.WithFields(log.Fields{
		"caldav_name":              caldavName,
		"calendar_name":            calendarName,
		"start":                    start.Format("2006-01-02T15:04:05"),
		"end":                      end.Format("2006-01-02T15:04:05"),
		"calendars_to_query":       calendarsToQuery,
		"time_window_back_days":    caldavCfg.TimeWindow.BackDays,
		"time_window_forward_days": caldavCfg.TimeWindow.ForwardDays,
	}).Info("fetching events from CalDAV")

	events, err := client.GetEvents(context.Background(), start, end, calendarsToQuery)
	if err != nil {
		l.WithError(err).WithFields(log.Fields{
			"caldav_name":   caldavName,
			"calendar_name": calendarName,
		}).Error("failed to get events")
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	l.WithFields(log.Fields{
		"caldav_name":    caldavName,
		"calendar_name":  calendarName,
		"events_count":   len(events),
	}).Info("fetched events from CalDAV")

	icsData, err := s.generator.GenerateBusyICS(events, start, end)
	if err != nil {
		l.WithError(err).WithFields(log.Fields{
			"caldav_name":   caldavName,
			"calendar_name": calendarName,
		}).Error("failed to generate busy ICS")
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	cache.SetWithKey(cacheKey, icsData)

	w.Header().Set("Content-Type", "text/calendar")
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(icsData))
}

func (s *Server) healthHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(`{"status":"ok"}`))
}

func (s *Server) Start() error {
	l := s.logger.WithField("fn", "Start")

	// Register routes for each CalDAV configuration
	http.HandleFunc("/", s.busyHandler)
	http.HandleFunc("/health", s.healthHandler)

	l.WithField("addr", s.config.Server.Address).Info("starting server")
	
	// Log available endpoints
	for _, caldav := range s.config.CalDAV {
		l.WithFields(log.Fields{
			"caldav_name": caldav.Name,
			"calendars":   caldav.Calendars,
			"combined":    caldav.Combined,
		}).Info("registered CalDAV configuration")
		
		// Log endpoint examples
		if caldav.Combined {
			l.WithField("endpoint", fmt.Sprintf("/%s/calendar.ics", caldav.Name)).Info("combined calendar endpoint")
		}
		for _, cal := range caldav.Calendars {
			l.WithField("endpoint", fmt.Sprintf("/%s/%s.ics", caldav.Name, cal)).Info("individual calendar endpoint")
		}
	}

	if err := http.ListenAndServe(s.config.Server.Address, nil); err != nil {
		l.WithError(err).Error("failed to start server")
		return err
	}

	return nil
}

func Start(addr string) error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("failed to load configuration: %w", err)
	}
	
	if addr != "" {
		cfg.Server.Address = addr
	}

	server, err := NewServer(cfg)
	if err != nil {
		return err
	}

	return server.Start()
}
