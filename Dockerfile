# Stage 1: Build
FROM golang:1.25 AS builder

# Set the working directory in the builder
WORKDIR /app

# Copy Go module files and download dependencies
COPY go.mod go.sum ./
RUN go mod download

# Copy the entire source code
COPY . .

# Set the working directory to the cmd directory
WORKDIR /app/cmd/api

# Build the Go application
RUN go build -o /saltybytes-api .

# Stage 2: Runtime
FROM debian:12-slim

# ffmpeg powers video-link import frame extraction; ca-certificates is needed
# for outbound HTTPS (it ships in distroless but not debian-slim).
RUN apt-get update \
    && apt-get install -y --no-install-recommends ca-certificates ffmpeg \
    && rm -rf /var/lib/apt/lists/*

# Copy the binary and config files from the builder
COPY --from=builder /saltybytes-api /saltybytes-api
COPY --from=builder /app/configs /configs

# Expose the API port
EXPOSE 8080

# Command to run the application
CMD ["/saltybytes-api"]
