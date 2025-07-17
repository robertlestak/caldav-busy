# CalDAV Busy Helm Chart

A Helm chart for deploying caldav-busy - a CalDAV free/busy service with multi-tenant support.

## Prerequisites

- Kubernetes 1.16+
- Helm 3.0+

## Installing the Chart

```bash
# Install with default values
helm install caldav-busy ./helm/caldav-busy

# Install with custom values
helm install caldav-busy ./helm/caldav-busy -f my-values.yaml
```

## Configuration

The chart uses a ConfigMap to store the application configuration. You can customize the configuration by modifying the `config` section in `values.yaml`.

### Required Secrets

You must create secrets for CalDAV passwords before installing the chart. The secret keys will be available as environment variables for substitution in the configuration:

```bash
kubectl create secret generic caldav-secrets \
  --from-literal=WORK_PASSWORD=YOUR_WORK_PASSWORD \
  --from-literal=PERSONAL_PASSWORD=YOUR_PERSONAL_PASSWORD \
  --from-literal=API_KEY=YOUR_API_KEY
```

### Custom Configuration

Edit the `config` section in `values.yaml`:

```yaml
config:
  server:
    address: ":8080"
    log_level: info
  
  defaults:
    refresh_interval: 15m
    time_window:
      back_days: 0
      forward_days: 30
  
  caldav:
    - name: personal
      url: https://cal.example.com/dav.php/principals/user/
      username: user
      password: ${PERSONAL_PASSWORD}
      calendars:
        - Events
        - "Default calendar"
      combined: true
    
    - name: work
      url: https://work.example.com/caldav/
      username: workuser
      password: ${WORK_PASSWORD}
      calendars:
        - Calendar
        - Meetings
      combined: false
```

## Values

| Key | Type | Default | Description |
|-----|------|---------|-------------|
| `replicaCount` | int | `1` | Number of replicas |
| `image.repository` | string | `"caldav-busy"` | Image repository |
| `image.pullPolicy` | string | `"IfNotPresent"` | Image pull policy |
| `image.tag` | string | `"latest"` | Image tag |
| `service.type` | string | `"ClusterIP"` | Service type |
| `service.port` | int | `8080` | Service port |
| `resources.limits.cpu` | string | `"500m"` | CPU limit |
| `resources.limits.memory` | string | `"512Mi"` | Memory limit |
| `resources.requests.cpu` | string | `"100m"` | CPU request |
| `resources.requests.memory` | string | `"128Mi"` | Memory request |
| `healthCheck.enabled` | bool | `true` | Enable health checks |
| `autoscaling.enabled` | bool | `false` | Enable horizontal pod autoscaling |
| `config` | object | See values.yaml | Application configuration |
| `envFrom` | array | See values.yaml | Environment variables from secrets |

## Uninstalling the Chart

```bash
helm uninstall caldav-busy
```

## Security

The chart follows Kubernetes security best practices:

- Runs as non-root user
- Uses read-only root filesystem
- Drops all capabilities
- Secrets are mounted as environment variables
- ServiceAccount with minimal permissions

## Accessing the Service

After installation, use port-forwarding to access the service:

```bash
kubectl port-forward svc/caldav-busy 8080:8080
```

Then access endpoints like:
- `http://localhost:8080/health`
- `http://localhost:8080/personal/calendar.ics`
- `http://localhost:8080/work/Calendar.ics`