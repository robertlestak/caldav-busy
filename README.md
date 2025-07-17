# caldav-busy

A powerful CalDAV client that retrieves calendar events from multiple CalDAV servers and provides ICS subscription URLs for busy time information that can be shared publicly. Perfect for multi-tenant deployments and complex calendar setups.

## Features

- **Multi-tenant Support** - Connect to multiple CalDAV servers in one instance
- **Flexible Endpoints** - Individual calendar endpoints + optional combined calendars
- **YAML Configuration** - Clean, readable configuration with environment variable support
- **Advanced Caching** - Per-configuration and per-calendar caching
- **Privacy-First** - Only exposes busy time slots, no event details
- **Production Ready** - Comprehensive error handling and logging

## Quick Start

1. **Create example configuration:**
   ```bash
   go run cmd/caldavbusy/caldavbusy.go -create-example -config config.yaml
   ```

2. **Edit configuration:**
   ```bash
   vim config.yaml
   ```

3. **Run the server:**
   ```bash
   go run cmd/caldavbusy/caldavbusy.go -config config.yaml
   ```

## Configuration

### YAML Configuration File

```yaml
server:
  address: ":8080"
  log_level: debug

defaults:
  refresh_interval: 15m # go time duration
  time_window:
    back_days: 0
    forward_days: 30

caldav:
- name: personal
  url: https://cal.example.com/dav.php/principals/user/
  username: user
  password: pass # uses os.ExpandEnv so you can use $ENV_VAR
  refresh_interval: 15m # go time duration
  time_window:
    back_days: 0
    forward_days: 30
  calendars:
  - Events
  - "Default calendar"
  combined: true  # enables /{name}/calendar.ics endpoint

- name: work
  url: https://work.example.com/caldav/
  username: workuser
  password: $WORK_PASSWORD
  calendars:
  - Calendar
  - Meetings
  combined: false  # only individual calendar endpoints enabled
```

### Environment Variables

Use environment variables in your configuration:

```bash
export WORK_PASSWORD="your-work-password"
export PERSONAL_PASSWORD="your-personal-password"
```

## API Endpoints

Based on the configuration above, the service generates:

### Personal Configuration (`name: personal`)
- `GET /personal/Events.ics` - Individual Events calendar
- `GET /personal/Default%20calendar.ics` - Individual Default calendar
- `GET /personal/calendar.ics` - Combined calendar (since `combined: true`)

### Work Configuration (`name: work`)
- `GET /work/Calendar.ics` - Individual Calendar
- `GET /work/Meetings.ics` - Individual Meetings calendar
- No combined endpoint (since `combined: false`)

### System Endpoints
- `GET /health` - Health check endpoint

## CLI Usage

```bash
# Create example configuration
./caldavbusy -create-example -config myconfig.yaml

# Run with default config.yaml
./caldavbusy

# Run with custom config
./caldavbusy -config myconfig.yaml

# Override server address
./caldavbusy -addr ":9000"

# Show help
./caldavbusy -h
```

## Build & Deploy

```bash
# Build binary
go build -o caldav-busy cmd/caldavbusy/caldavbusy.go

# Run with config
./caldav-busy -config production.yaml
```

## Advanced Features

### Multi-Tenant Architecture

- **Isolated Configurations** - Each CalDAV server has its own settings
- **Independent Caching** - Separate cache per configuration and calendar
- **Flexible Time Windows** - Different time ranges per configuration
- **Environment Variables** - Secure password management with `$ENV_VAR`

### Performance Features

- **Smart Caching** - Per-calendar cache with configurable refresh intervals
- **Efficient Recurrence** - Optimized RRULE expansion with safety limits
- **Memory Efficient** - Streaming event processing
- **Fast Forwarding** - Efficient range calculations for large date ranges

## Configuration Reference

### Server Configuration
```yaml
server:
  address: ":8080"          # Server bind address
  log_level: "info"         # debug, info, warn, error
```

### Default Settings
```yaml
defaults:
  refresh_interval: 15m     # Cache refresh interval
  time_window:
    back_days: 0            # Days back from today
    forward_days: 30        # Days forward from today
```

### CalDAV Configuration
```yaml
caldav:
- name: "unique-name"       # Required: Unique configuration name
  url: "https://..."        # Required: CalDAV server URL
  username: "user"          # Required: CalDAV username
  password: "pass"          # Required: CalDAV password (supports $ENV_VAR)
  refresh_interval: 15m     # Optional: Override default refresh interval
  time_window:              # Optional: Override default time window
    back_days: 0
    forward_days: 30
  calendars:                # Required: List of calendars to include
  - "Calendar Name"
  combined: true            # Optional: Enable /{name}/calendar.ics endpoint
```

## Privacy & Security

This application is designed with privacy in mind:

✅ **Only exposes busy time slots** - No event details
✅ **No sensitive data** - Event titles, descriptions, locations not shared
✅ **Secure authentication** - CalDAV credentials stored securely
✅ **Environment variables** - Passwords can be injected via environment

**Never exposes:**
- Event titles
- Event descriptions
- Event locations
- Attendee information
- Any other private calendar data

## Troubleshooting

### Common Issues

1. **Configuration not found:**
   ```bash
   ./caldavbusy -create-example -config config.yaml
   ```

2. **CalDAV authentication issues:**
   - Check username/password
   - Verify CalDAV URL format
   - Check server supports CalDAV

3. **Calendar not found:**
   - Verify calendar names in configuration
   - Check CalDAV server calendar list

4. **Debug logging:**
   ```yaml
   server:
     log_level: debug
   ```

## License

MIT License
