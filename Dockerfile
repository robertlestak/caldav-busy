# Build stage
FROM golang:1.23.4-alpine AS builder

WORKDIR /app

# Install git for go modules
RUN apk add --no-cache git

# Copy go mod files
COPY go.mod go.sum ./

# Download dependencies
RUN go mod download

# Copy source code
COPY . .

# Build the application
RUN CGO_ENABLED=0 GOOS=linux go build -a -installsuffix cgo -o caldav-busy cmd/caldavbusy/caldavbusy.go

# Final stage
FROM alpine:latest

# Install ca-certificates for HTTPS connections and timezone data for date handling
RUN apk --no-cache add ca-certificates \
    tzdata \
    && update-ca-certificates

WORKDIR /root/

# Copy the binary from builder stage
COPY --from=builder /app/caldav-busy /bin/caldav-busy

# Create config directory
RUN mkdir -p /config

# Expose port
EXPOSE 8080

# Command to run
ENTRYPOINT ["/bin/caldav-busy"]