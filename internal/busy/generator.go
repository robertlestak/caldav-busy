package busy

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/emersion/go-ical"
	"github.com/emersion/go-webdav/caldav"
	log "github.com/sirupsen/logrus"
)

type BusyGenerator struct {
	logger *log.Entry
}

func NewGenerator() *BusyGenerator {
	return &BusyGenerator{
		logger: log.WithField("module", "busy"),
	}
}

type TimeSlot struct {
	Start time.Time
	End   time.Time
}

func (g *BusyGenerator) GenerateBusyICS(events []caldav.CalendarObject, start, end time.Time) (string, error) {
	l := g.logger.WithField("fn", "GenerateBusyICS")

	l.WithFields(log.Fields{
		"events_count": len(events),
		"start":        start.Format("2006-01-02T15:04:05"),
		"end":          end.Format("2006-01-02T15:04:05"),
	}).Info("processing events for busy generation")

	// Expand recurring events first
	busySlots := g.expandRecurringEvents(events, start, end)
	busySlots = g.mergeBusySlots(busySlots)

	l.WithField("slots", len(busySlots)).Info("generated busy slots")

	calendar := ical.NewCalendar()
	calendar.Props.SetText(ical.PropVersion, "2.0")
	calendar.Props.SetText(ical.PropProductID, "-//caldav-busy//EN")
	calendar.Props.SetText(ical.PropMethod, "PUBLISH")
	calendar.Props.SetText(ical.PropCalendarScale, "GREGORIAN")

	// Create individual VEVENT entries instead of VFREEBUSY
	for i, slot := range busySlots {
		if slot.Start.Before(end) && slot.End.After(start) {
			adjustedStart := slot.Start
			adjustedEnd := slot.End
			if adjustedStart.Before(start) {
				adjustedStart = start
			}
			if adjustedEnd.After(end) {
				adjustedEnd = end
			}

			// Create a VEVENT for this busy slot
			now := time.Now().UTC()
			event := ical.NewComponent(ical.CompEvent)
			event.Props.SetDateTime(ical.PropDateTimeStamp, now)
			// Force UTC times for better Outlook compatibility
			event.Props.SetDateTime(ical.PropDateTimeStart, adjustedStart.UTC())
			event.Props.SetDateTime(ical.PropDateTimeEnd, adjustedEnd.UTC())
			event.Props.SetDateTime(ical.PropCreated, now)
			event.Props.SetDateTime(ical.PropLastModified, now)
			event.Props.SetText(ical.PropUID, fmt.Sprintf("busy-slot-%d-%d@caldav-busy", now.Unix(), i))
			event.Props.SetText(ical.PropSummary, "Busy")
			event.Props.SetText(ical.PropStatus, "CONFIRMED")
			event.Props.SetText(ical.PropClass, "PUBLIC")

			// Add transparency to show as busy
			event.Props.SetText(ical.PropTransparency, "OPAQUE")

			calendar.Children = append(calendar.Children, event)
		}
	}

	// If no events were added, the calendar encoder might fail
	if len(calendar.Children) == 0 {
		l.Info("no busy slots found, returning empty calendar")
		// Return a minimal valid ICS calendar
		return "BEGIN:VCALENDAR\nVERSION:2.0\nPRODID:-//caldav-busy//EN\nMETHOD:PUBLISH\nCALSCALE:GREGORIAN\nEND:VCALENDAR\n", nil
	}

	var buf strings.Builder
	if err := ical.NewEncoder(&buf).Encode(calendar); err != nil {
		l.WithError(err).Error("failed to encode calendar")
		return "", err
	}

	return buf.String(), nil
}

func (g *BusyGenerator) expandRecurringEvents(events []caldav.CalendarObject, start, end time.Time) []TimeSlot {
	var slots []TimeSlot
	l := g.logger.WithField("fn", "expandRecurringEvents")

	for i, event := range events {
		l.WithFields(log.Fields{
			"event_index": i,
			"event_path":  event.Path,
		}).Debug("checking event for recurrence")

		calendar := event.Data
		if calendar == nil {
			continue
		}

		for j, component := range calendar.Children {
			if component.Name != ical.CompEvent {
				continue
			}

			// Get the original event start and end times
			dtstart, err := component.Props.DateTime(ical.PropDateTimeStart, nil)
			if err != nil {
				l.WithError(err).Error("failed to parse event start time")
				continue
			}

			dtend, err := component.Props.DateTime(ical.PropDateTimeEnd, nil)
			if err != nil {
				l.WithError(err).Error("failed to parse event end time")
				continue
			}

			// Check transparency - skip transparent events
			transp := component.Props.Get(ical.PropTransparency)
			if transp != nil && transp.Value == "TRANSPARENT" {
				l.WithFields(log.Fields{
					"event_index":     i,
					"component_index": j,
				}).Debug("skipping transparent event")
				continue
			}

			// Check if event has RRULE (recurring rule)
			rrule := component.Props.Get("RRULE")
			if rrule == nil {
				// Not a recurring event, add single occurrence if it's within range
				if dtstart.Before(end) && dtend.After(start) {
					slots = append(slots, TimeSlot{Start: dtstart, End: dtend})
				}
				continue
			}

			// Parse RRULE and expand recurring events
			expandedSlots := g.expandRRule(rrule.Value, dtstart, dtend, start, end)
			slots = append(slots, expandedSlots...)

			l.WithFields(log.Fields{
				"event_index":        i,
				"component_index":    j,
				"rrule":              rrule.Value,
				"expanded_instances": len(expandedSlots),
			}).Info("expanded recurring event")
		}
	}

	l.WithField("total_slots", len(slots)).Info("completed recurring event expansion")
	return slots
}

func (g *BusyGenerator) expandRRule(rruleStr string, originalStart, originalEnd, rangeStart, rangeEnd time.Time) []TimeSlot {
	var slots []TimeSlot
	l := g.logger.WithField("fn", "expandRRule")

	// Parse RRULE
	rrule := g.parseRRule(rruleStr)
	duration := originalEnd.Sub(originalStart)

	l.WithFields(log.Fields{
		"rrule":          rruleStr,
		"original_start": originalStart.Format("2006-01-02T15:04:05"),
		"duration":       duration.String(),
		"range_start":    rangeStart.Format("2006-01-02T15:04:05"),
		"range_end":      rangeEnd.Format("2006-01-02T15:04:05"),
	}).Debug("expanding RRULE")

	// Generate occurrences based on frequency
	switch rrule.Freq {
	case "SECONDLY":
		slots = g.expandSecondlyRule(rrule, originalStart, duration, rangeStart, rangeEnd)
	case "MINUTELY":
		slots = g.expandMinutelyRule(rrule, originalStart, duration, rangeStart, rangeEnd)
	case "HOURLY":
		slots = g.expandHourlyRule(rrule, originalStart, duration, rangeStart, rangeEnd)
	case "DAILY":
		slots = g.expandDailyRule(rrule, originalStart, duration, rangeStart, rangeEnd)
	case "WEEKLY":
		slots = g.expandWeeklyRule(rrule, originalStart, duration, rangeStart, rangeEnd)
	case "MONTHLY":
		slots = g.expandMonthlyRule(rrule, originalStart, duration, rangeStart, rangeEnd)
	case "YEARLY":
		slots = g.expandYearlyRule(rrule, originalStart, duration, rangeStart, rangeEnd)
	default:
		l.WithField("freq", rrule.Freq).Warn("unsupported RRULE frequency")
		// Fall back to single occurrence
		if originalStart.Before(rangeEnd) && originalEnd.After(rangeStart) {
			slots = append(slots, TimeSlot{Start: originalStart, End: originalEnd})
		}
	}

	// Apply BYSETPOS filtering if specified
	if len(rrule.BySetPos) > 0 {
		slots = g.applyBySetPos(slots, rrule.BySetPos)
	}

	return slots
}

type RRule struct {
	Freq       string       // SECONDLY, MINUTELY, HOURLY, DAILY, WEEKLY, MONTHLY, YEARLY
	Interval   int          // How often the rule repeats (default: 1)
	Count      int          // Maximum number of occurrences
	Until      *time.Time   // End date/time for the rule
	ByDay      []string     // Day of week (MO,TU,WE,TH,FR,SA,SU) with optional ordinals
	ByMonth    []int        // Month (1-12)
	ByMonthDay []int        // Day of month (1-31, -31 to -1)
	ByYearDay  []int        // Day of year (1-366, -366 to -1)
	ByWeekNo   []int        // Week of year (1-53, -53 to -1)
	ByHour     []int        // Hour (0-23)
	ByMinute   []int        // Minute (0-59)
	BySecond   []int        // Second (0-60)
	BySetPos   []int        // Limit results to specific positions
	WkSt       time.Weekday // First day of week (default: Monday)
}

func (g *BusyGenerator) parseRRule(rruleStr string) RRule {
	rrule := RRule{
		Interval: 1,
		WkSt:     time.Monday, // Default first day of week
	}

	parts := strings.Split(rruleStr, ";")
	for _, part := range parts {
		kv := strings.Split(part, "=")
		if len(kv) != 2 {
			continue
		}

		key, value := kv[0], kv[1]
		switch key {
		case "FREQ":
			rrule.Freq = value
		case "INTERVAL":
			if interval, err := strconv.Atoi(value); err == nil {
				rrule.Interval = interval
			}
		case "COUNT":
			if count, err := strconv.Atoi(value); err == nil {
				rrule.Count = count
			}
		case "UNTIL":
			// Try different date formats according to RFC 5545
			formats := []string{
				"20060102T150405Z",     // UTC format
				"20060102T150405",      // Local time format
				"20060102",             // Date only format
				"2006-01-02T15:04:05Z", // ISO 8601 UTC
				"2006-01-02T15:04:05",  // ISO 8601 local
				"2006-01-02",           // ISO 8601 date only
			}
			for _, format := range formats {
				if until, err := time.Parse(format, value); err == nil {
					rrule.Until = &until
					break
				}
			}
		case "BYDAY":
			rrule.ByDay = strings.Split(value, ",")
		case "BYMONTH":
			rrule.ByMonth = parseIntList(value)
		case "BYMONTHDAY":
			rrule.ByMonthDay = parseIntList(value)
		case "BYYEARDAY":
			rrule.ByYearDay = parseIntList(value)
		case "BYWEEKNO":
			rrule.ByWeekNo = parseIntList(value)
		case "BYHOUR":
			rrule.ByHour = parseIntList(value)
		case "BYMINUTE":
			rrule.ByMinute = parseIntList(value)
		case "BYSECOND":
			rrule.BySecond = parseIntList(value)
		case "BYSETPOS":
			rrule.BySetPos = parseIntList(value)
		case "WKST":
			rrule.WkSt = parseWeekday(value)
		}
	}

	return rrule
}

func parseIntList(value string) []int {
	parts := strings.Split(value, ",")
	var result []int
	for _, part := range parts {
		if num, err := strconv.Atoi(strings.TrimSpace(part)); err == nil {
			result = append(result, num)
		}
	}
	return result
}

func parseWeekday(value string) time.Weekday {
	switch value {
	case "SU":
		return time.Sunday
	case "MO":
		return time.Monday
	case "TU":
		return time.Tuesday
	case "WE":
		return time.Wednesday
	case "TH":
		return time.Thursday
	case "FR":
		return time.Friday
	case "SA":
		return time.Saturday
	default:
		return time.Monday // Default
	}
}

func (g *BusyGenerator) expandWeeklyRule(rrule RRule, originalStart time.Time, duration time.Duration, rangeStart, rangeEnd time.Time) []TimeSlot {
	var slots []TimeSlot

	// Map day abbreviations to weekday numbers
	dayMap := map[string]time.Weekday{
		"SU": time.Sunday,
		"MO": time.Monday,
		"TU": time.Tuesday,
		"WE": time.Wednesday,
		"TH": time.Thursday,
		"FR": time.Friday,
		"SA": time.Saturday,
	}

	// If no BYDAY specified, use the original start day
	targetDays := []time.Weekday{originalStart.Weekday()}
	if len(rrule.ByDay) > 0 {
		targetDays = nil
		for _, day := range rrule.ByDay {
			if len(day) == 2 {
				if weekday, ok := dayMap[day]; ok {
					targetDays = append(targetDays, weekday)
				}
			}
		}
	}

	// Start from the beginning of the week containing rangeStart
	// Adjust for WKST (week start day)
	weekStart := int(rrule.WkSt)
	currentWeekday := int(rangeStart.Weekday())

	// Calculate days to go back to reach the week start
	daysBack := (currentWeekday - weekStart + 7) % 7
	current := rangeStart.AddDate(0, 0, -daysBack)

	// Set the time to match the original event
	year, month, day := current.Date()
	hour, min, sec := originalStart.Clock()
	loc := originalStart.Location()
	current = time.Date(year, month, day, hour, min, sec, originalStart.Nanosecond(), loc)

	count := 0
	for current.Before(rangeEnd) {
		if rrule.Count > 0 && count >= rrule.Count {
			break
		}
		if rrule.Until != nil && current.After(*rrule.Until) {
			break
		}

		// Check each target day in the current week
		for _, targetDay := range targetDays {
			// Calculate the date for this target day
			daysUntilTarget := int(targetDay - current.Weekday())
			if daysUntilTarget < 0 {
				daysUntilTarget += 7
			}

			eventStart := current.AddDate(0, 0, daysUntilTarget)

			// Check if this occurrence matches all BY-rules
			if g.matchesByRules(eventStart, rrule) {
				eventEnd := eventStart.Add(duration)
				// Only add if within range
				if eventStart.Before(rangeEnd) && eventEnd.After(rangeStart) {
					slots = append(slots, TimeSlot{Start: eventStart, End: eventEnd})
					count++
				}
			}
		}

		// Move to next week (interval weeks)
		current = current.AddDate(0, 0, 7*rrule.Interval)
	}

	return slots
}

func (g *BusyGenerator) expandDailyRule(rrule RRule, originalStart time.Time, duration time.Duration, rangeStart, rangeEnd time.Time) []TimeSlot {
	var slots []TimeSlot

	current := originalStart
	if current.Before(rangeStart) {
		// Fast forward to rangeStart
		days := int(rangeStart.Sub(current).Hours() / 24)
		current = current.AddDate(0, 0, days)
	}

	count := 0
	for current.Before(rangeEnd) {
		if rrule.Count > 0 && count >= rrule.Count {
			break
		}
		if rrule.Until != nil && current.After(*rrule.Until) {
			break
		}

		if g.matchesByRules(current, rrule) {
			eventEnd := current.Add(duration)
			if current.Before(rangeEnd) && eventEnd.After(rangeStart) {
				slots = append(slots, TimeSlot{Start: current, End: eventEnd})
				count++
			}
		}

		current = current.AddDate(0, 0, rrule.Interval)
	}

	return slots
}

func (g *BusyGenerator) expandMonthlyRule(rrule RRule, originalStart time.Time, duration time.Duration, rangeStart, rangeEnd time.Time) []TimeSlot {
	var slots []TimeSlot

	// Start from the original start time, but adjust to the range if needed
	current := originalStart
	if current.Before(rangeStart) {
		// Fast forward to the month containing rangeStart
		yearDiff := rangeStart.Year() - current.Year()
		monthDiff := int(rangeStart.Month()) - int(current.Month())
		totalMonths := yearDiff*12 + monthDiff

		// Round down to the nearest interval
		intervalMonths := (totalMonths / rrule.Interval) * rrule.Interval
		current = current.AddDate(0, intervalMonths, 0)

		// If we're still before rangeStart, move to next interval
		if current.Before(rangeStart) {
			current = current.AddDate(0, rrule.Interval, 0)
		}
	}

	count := 0
	for current.Before(rangeEnd) {
		if rrule.Count > 0 && count >= rrule.Count {
			break
		}
		if rrule.Until != nil && current.After(*rrule.Until) {
			break
		}

		if g.matchesByRules(current, rrule) {
			eventEnd := current.Add(duration)
			if current.Before(rangeEnd) && eventEnd.After(rangeStart) {
				slots = append(slots, TimeSlot{Start: current, End: eventEnd})
				count++
			}
		}

		// Move to next month (interval months)
		current = current.AddDate(0, rrule.Interval, 0)
	}

	return slots
}

func (g *BusyGenerator) expandSecondlyRule(rrule RRule, originalStart time.Time, duration time.Duration, rangeStart, rangeEnd time.Time) []TimeSlot {
	var slots []TimeSlot

	current := originalStart
	if current.Before(rangeStart) {
		// Fast forward to rangeStart
		seconds := int(rangeStart.Sub(current).Seconds())
		current = current.Add(time.Duration(seconds) * time.Second)
	}

	count := 0
	for current.Before(rangeEnd) && count < 10000 { // Safety limit for secondly rules
		if rrule.Count > 0 && count >= rrule.Count {
			break
		}
		if rrule.Until != nil && current.After(*rrule.Until) {
			break
		}

		if g.matchesByRules(current, rrule) {
			eventEnd := current.Add(duration)
			if current.Before(rangeEnd) && eventEnd.After(rangeStart) {
				slots = append(slots, TimeSlot{Start: current, End: eventEnd})
				count++
			}
		}

		current = current.Add(time.Duration(rrule.Interval) * time.Second)
	}

	return slots
}

func (g *BusyGenerator) expandMinutelyRule(rrule RRule, originalStart time.Time, duration time.Duration, rangeStart, rangeEnd time.Time) []TimeSlot {
	var slots []TimeSlot

	current := originalStart
	if current.Before(rangeStart) {
		// Fast forward to rangeStart
		minutes := int(rangeStart.Sub(current).Minutes())
		current = current.Add(time.Duration(minutes) * time.Minute)
	}

	count := 0
	for current.Before(rangeEnd) && count < 10000 { // Safety limit
		if rrule.Count > 0 && count >= rrule.Count {
			break
		}
		if rrule.Until != nil && current.After(*rrule.Until) {
			break
		}

		if g.matchesByRules(current, rrule) {
			eventEnd := current.Add(duration)
			if current.Before(rangeEnd) && eventEnd.After(rangeStart) {
				slots = append(slots, TimeSlot{Start: current, End: eventEnd})
				count++
			}
		}

		current = current.Add(time.Duration(rrule.Interval) * time.Minute)
	}

	return slots
}

func (g *BusyGenerator) expandHourlyRule(rrule RRule, originalStart time.Time, duration time.Duration, rangeStart, rangeEnd time.Time) []TimeSlot {
	var slots []TimeSlot

	current := originalStart
	if current.Before(rangeStart) {
		// Fast forward to rangeStart
		hours := int(rangeStart.Sub(current).Hours())
		current = current.Add(time.Duration(hours) * time.Hour)
	}

	count := 0
	for current.Before(rangeEnd) && count < 10000 { // Safety limit
		if rrule.Count > 0 && count >= rrule.Count {
			break
		}
		if rrule.Until != nil && current.After(*rrule.Until) {
			break
		}

		if g.matchesByRules(current, rrule) {
			eventEnd := current.Add(duration)
			if current.Before(rangeEnd) && eventEnd.After(rangeStart) {
				slots = append(slots, TimeSlot{Start: current, End: eventEnd})
				count++
			}
		}

		current = current.Add(time.Duration(rrule.Interval) * time.Hour)
	}

	return slots
}

func (g *BusyGenerator) expandYearlyRule(rrule RRule, originalStart time.Time, duration time.Duration, rangeStart, rangeEnd time.Time) []TimeSlot {
	var slots []TimeSlot

	current := originalStart
	if current.Before(rangeStart) {
		// Fast forward to the year containing rangeStart
		years := rangeStart.Year() - current.Year()
		current = current.AddDate(years, 0, 0)

		// If we're still before rangeStart, move to next year
		if current.Before(rangeStart) {
			current = current.AddDate(rrule.Interval, 0, 0)
		}
	}

	count := 0
	for current.Before(rangeEnd) {
		if rrule.Count > 0 && count >= rrule.Count {
			break
		}
		if rrule.Until != nil && current.After(*rrule.Until) {
			break
		}

		if g.matchesByRules(current, rrule) {
			eventEnd := current.Add(duration)
			if current.Before(rangeEnd) && eventEnd.After(rangeStart) {
				slots = append(slots, TimeSlot{Start: current, End: eventEnd})
				count++
			}
		}

		current = current.AddDate(rrule.Interval, 0, 0)
	}

	return slots
}

// matchesByRules checks if a time matches all the BY-rules
func (g *BusyGenerator) matchesByRules(t time.Time, rrule RRule) bool {
	// BYMONTH
	if len(rrule.ByMonth) > 0 {
		month := int(t.Month())
		found := false
		for _, m := range rrule.ByMonth {
			if m == month {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}

	// BYMONTHDAY
	if len(rrule.ByMonthDay) > 0 {
		day := t.Day()
		found := false
		for _, d := range rrule.ByMonthDay {
			if d > 0 && d == day {
				found = true
				break
			} else if d < 0 {
				// Negative values count from end of month
				lastDay := time.Date(t.Year(), t.Month()+1, 0, 0, 0, 0, 0, t.Location()).Day()
				if day == lastDay+d+1 {
					found = true
					break
				}
			}
		}
		if !found {
			return false
		}
	}

	// BYDAY
	if len(rrule.ByDay) > 0 {
		weekday := t.Weekday()
		found := false
		for _, dayStr := range rrule.ByDay {
			if len(dayStr) == 2 {
				// Simple weekday matching
				if g.matchesWeekday(weekday, dayStr) {
					found = true
					break
				}
			} else if len(dayStr) >= 3 {
				// Ordinal weekday matching
				if g.matchesOrdinalWeekday(t, dayStr, rrule.Freq) {
					found = true
					break
				}
			}
		}
		if !found {
			return false
		}
	}

	// BYHOUR
	if len(rrule.ByHour) > 0 {
		hour := t.Hour()
		found := false
		for _, h := range rrule.ByHour {
			if h == hour {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}

	// BYMINUTE
	if len(rrule.ByMinute) > 0 {
		minute := t.Minute()
		found := false
		for _, m := range rrule.ByMinute {
			if m == minute {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}

	// BYSECOND
	if len(rrule.BySecond) > 0 {
		second := t.Second()
		found := false
		for _, s := range rrule.BySecond {
			if s == second {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}

	// BYYEARDAY
	if len(rrule.ByYearDay) > 0 {
		yearDay := t.YearDay()
		found := false
		for _, yd := range rrule.ByYearDay {
			if yd > 0 && yd == yearDay {
				found = true
				break
			} else if yd < 0 {
				// Negative values count from end of year
				daysInYear := 365
				if isLeapYear(t.Year()) {
					daysInYear = 366
				}
				if yearDay == daysInYear+yd+1 {
					found = true
					break
				}
			}
		}
		if !found {
			return false
		}
	}

	// BYWEEKNO
	if len(rrule.ByWeekNo) > 0 {
		_, weekNo := t.ISOWeek()
		found := false
		for _, wn := range rrule.ByWeekNo {
			if wn > 0 && wn == weekNo {
				found = true
				break
			} else if wn < 0 {
				// Negative values count from end of year
				_, lastWeek := time.Date(t.Year(), 12, 31, 0, 0, 0, 0, t.Location()).ISOWeek()
				if weekNo == lastWeek+wn+1 {
					found = true
					break
				}
			}
		}
		if !found {
			return false
		}
	}

	return true
}

func (g *BusyGenerator) matchesWeekday(weekday time.Weekday, dayStr string) bool {
	// Handle simple weekday matching (MO, TU, WE, etc.)
	if len(dayStr) == 2 {
		return parseWeekday(dayStr) == weekday
	}

	// Handle ordinal weekdays (1MO, -1FR, etc.)
	if len(dayStr) >= 3 {
		// Parse ordinal and weekday
		weekdayStr := dayStr[len(dayStr)-2:]

		targetWeekday := parseWeekday(weekdayStr)
		if targetWeekday != weekday {
			return false
		}

		// For ordinal weekdays, we need additional context (current time)
		// This is a simplified implementation
		// Full RFC implementation would require passing the current time
		// and checking if it's the Nth occurrence of the weekday
		return true
	}

	return false
}

// matchesOrdinalWeekday checks if a time matches an ordinal weekday pattern (1MO, -1FR, etc.)
func (g *BusyGenerator) matchesOrdinalWeekday(t time.Time, dayStr string, freq string) bool {
	if len(dayStr) < 3 {
		return false
	}

	ordinalStr := dayStr[:len(dayStr)-2]
	weekdayStr := dayStr[len(dayStr)-2:]

	ordinal, err := strconv.Atoi(ordinalStr)
	if err != nil {
		return false
	}

	targetWeekday := parseWeekday(weekdayStr)
	if t.Weekday() != targetWeekday {
		return false
	}

	// Check ordinal position based on frequency
	switch freq {
	case "MONTHLY":
		return g.isNthWeekdayInMonth(t, ordinal, targetWeekday)
	case "YEARLY":
		return g.isNthWeekdayInYear(t, ordinal, targetWeekday)
	default:
		return true // For other frequencies, simplified check
	}
}

// isNthWeekdayInMonth checks if the date is the Nth occurrence of weekday in the month
func (g *BusyGenerator) isNthWeekdayInMonth(t time.Time, ordinal int, weekday time.Weekday) bool {
	firstOfMonth := time.Date(t.Year(), t.Month(), 1, 0, 0, 0, 0, t.Location())

	if ordinal > 0 {
		// Find the Nth occurrence from the beginning of the month
		current := firstOfMonth
		count := 0
		for current.Month() == t.Month() {
			if current.Weekday() == weekday {
				count++
				if count == ordinal {
					return current.Day() == t.Day()
				}
			}
			current = current.AddDate(0, 0, 1)
		}
	} else if ordinal < 0 {
		// Find the Nth occurrence from the end of the month
		lastOfMonth := time.Date(t.Year(), t.Month()+1, 0, 0, 0, 0, 0, t.Location())
		current := lastOfMonth
		count := 0
		for current.Month() == t.Month() {
			if current.Weekday() == weekday {
				count++
				if count == -ordinal {
					return current.Day() == t.Day()
				}
			}
			current = current.AddDate(0, 0, -1)
		}
	}

	return false
}

// isNthWeekdayInYear checks if the date is the Nth occurrence of weekday in the year
func (g *BusyGenerator) isNthWeekdayInYear(t time.Time, ordinal int, weekday time.Weekday) bool {
	firstOfYear := time.Date(t.Year(), 1, 1, 0, 0, 0, 0, t.Location())

	if ordinal > 0 {
		// Find the Nth occurrence from the beginning of the year
		current := firstOfYear
		count := 0
		for current.Year() == t.Year() {
			if current.Weekday() == weekday {
				count++
				if count == ordinal {
					return current.YearDay() == t.YearDay()
				}
			}
			current = current.AddDate(0, 0, 1)
		}
	} else if ordinal < 0 {
		// Find the Nth occurrence from the end of the year
		lastOfYear := time.Date(t.Year(), 12, 31, 0, 0, 0, 0, t.Location())
		current := lastOfYear
		count := 0
		for current.Year() == t.Year() {
			if current.Weekday() == weekday {
				count++
				if count == -ordinal {
					return current.YearDay() == t.YearDay()
				}
			}
			current = current.AddDate(0, 0, -1)
		}
	}

	return false
}

func isLeapYear(year int) bool {
	return year%4 == 0 && (year%100 != 0 || year%400 == 0)
}

// applyBySetPos applies BYSETPOS filtering to limit results to specific positions
func (g *BusyGenerator) applyBySetPos(slots []TimeSlot, bySetPos []int) []TimeSlot {
	if len(slots) == 0 || len(bySetPos) == 0 {
		return slots
	}

	var result []TimeSlot
	slotCount := len(slots)

	for _, pos := range bySetPos {
		var index int
		if pos > 0 {
			// Positive position (1-based)
			index = pos - 1
		} else if pos < 0 {
			// Negative position (count from end)
			index = slotCount + pos
		} else {
			// Position 0 is not valid
			continue
		}

		// Check if index is valid
		if index >= 0 && index < slotCount {
			result = append(result, slots[index])
		}
	}

	return result
}

func (g *BusyGenerator) mergeBusySlots(slots []TimeSlot) []TimeSlot {
	if len(slots) == 0 {
		return slots
	}

	sort.Slice(slots, func(i, j int) bool {
		return slots[i].Start.Before(slots[j].Start)
	})

	var merged []TimeSlot
	current := slots[0]

	for i := 1; i < len(slots); i++ {
		next := slots[i]

		if current.End.After(next.Start) || current.End.Equal(next.Start) {
			if next.End.After(current.End) {
				current.End = next.End
			}
		} else {
			merged = append(merged, current)
			current = next
		}
	}

	merged = append(merged, current)
	return merged
}
