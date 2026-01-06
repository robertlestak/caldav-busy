package config

import (
	"fmt"
	"os"
	"time"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Server   ServerConfig   `yaml:"server"`
	Defaults DefaultsConfig `yaml:"defaults"`
	CalDAV   []CalDAVConfig `yaml:"caldav"`
}

type ServerConfig struct {
	Address  string `yaml:"address"`
	LogLevel string `yaml:"log_level"`
}

type DefaultsConfig struct {
	RefreshInterval Duration    `yaml:"refresh_interval"`
	TimeWindow      TimeWindow  `yaml:"time_window"`
}

type TimeWindow struct {
	BackDays    int `yaml:"back_days"`
	ForwardDays int `yaml:"forward_days"`
}

type CalDAVConfig struct {
	Name            string     `yaml:"name"`
	URL             string     `yaml:"url"`
	Username        string     `yaml:"username"`
	Password        string     `yaml:"password"`
	RefreshInterval Duration   `yaml:"refresh_interval,omitempty"`
	TimeWindow      TimeWindow `yaml:"time_window,omitempty"`
	Calendars       []string   `yaml:"calendars"`
	Combined        bool       `yaml:"combined,omitempty"`
}

// Duration wrapper for time.Duration to support YAML parsing
type Duration time.Duration

func (d *Duration) UnmarshalYAML(value *yaml.Node) error {
	var s string
	if err := value.Decode(&s); err != nil {
		return err
	}
	dur, err := time.ParseDuration(s)
	if err != nil {
		return err
	}
	*d = Duration(dur)
	return nil
}

func (d Duration) Duration() time.Duration {
	return time.Duration(d)
}

func Load() (*Config, error) {
	return LoadFromFile("config.yaml")
}

func LoadFromFile(filename string) (*Config, error) {
	// Check if file exists
	if _, err := os.Stat(filename); os.IsNotExist(err) {
		return nil, fmt.Errorf("config file %s not found", filename)
	}

	// Read file
	data, err := os.ReadFile(filename)
	if err != nil {
		return nil, fmt.Errorf("failed to read config file: %w", err)
	}

	// Parse YAML
	var config Config
	if err := yaml.Unmarshal(data, &config); err != nil {
		return nil, fmt.Errorf("failed to parse YAML config: %w", err)
	}

	// Validate and apply defaults
	if err := config.validate(); err != nil {
		return nil, fmt.Errorf("config validation failed: %w", err)
	}

	// Expand environment variables in passwords
	for i := range config.CalDAV {
		config.CalDAV[i].Password = os.ExpandEnv(config.CalDAV[i].Password)
	}

	return &config, nil
}

func (c *Config) validate() error {
	// Set defaults
	if c.Server.Address == "" {
		c.Server.Address = ":8080"
	}
	if c.Server.LogLevel == "" {
		c.Server.LogLevel = "info"
	}
	if c.Defaults.RefreshInterval == 0 {
		c.Defaults.RefreshInterval = Duration(15 * time.Minute)
	}
	if c.Defaults.TimeWindow.BackDays == 0 && c.Defaults.TimeWindow.ForwardDays == 0 {
		c.Defaults.TimeWindow.BackDays = 0
		c.Defaults.TimeWindow.ForwardDays = 30
	}

	// Validate CalDAV configurations
	if len(c.CalDAV) == 0 {
		return fmt.Errorf("at least one CalDAV configuration is required")
	}

	names := make(map[string]bool)
	for i, caldav := range c.CalDAV {
		if caldav.Name == "" {
			return fmt.Errorf("CalDAV config %d: name is required", i)
		}
		if names[caldav.Name] {
			return fmt.Errorf("CalDAV config %d: duplicate name '%s'", i, caldav.Name)
		}
		names[caldav.Name] = true

		if caldav.URL == "" {
			return fmt.Errorf("CalDAV config '%s': URL is required", caldav.Name)
		}
		if caldav.Username == "" {
			return fmt.Errorf("CalDAV config '%s': username is required", caldav.Name)
		}
		if caldav.Password == "" {
			return fmt.Errorf("CalDAV config '%s': password is required", caldav.Name)
		}

		// Apply defaults if not specified
		if caldav.RefreshInterval == 0 {
			c.CalDAV[i].RefreshInterval = c.Defaults.RefreshInterval
		}
		if caldav.TimeWindow.BackDays == 0 && caldav.TimeWindow.ForwardDays == 0 {
			c.CalDAV[i].TimeWindow = c.Defaults.TimeWindow
		}
	}

	return nil
}

func (c *Config) GetCalDAVByName(name string) (*CalDAVConfig, error) {
	for _, caldav := range c.CalDAV {
		if caldav.Name == name {
			return &caldav, nil
		}
	}
	return nil, fmt.Errorf("CalDAV configuration '%s' not found", name)
}

func (c *Config) GetCalDAVNames() []string {
	names := make([]string, len(c.CalDAV))
	for i, caldav := range c.CalDAV {
		names[i] = caldav.Name
	}
	return names
}

func CreateExample(filename string) error {
	example := `server:
  address: ":8080"
  log_level: debug

defaults:
  refresh_interval: 15m # go time duration
  time_window:
    back_days: 0
    forward_days: 30

caldav:
- name: foobar
  url: https://cal.lestak.sh/dav.php/principals/rob/
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
`

	return os.WriteFile(filename, []byte(example), 0644)
}