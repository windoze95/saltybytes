# Stage 1: Build
FROM golang:1.24 AS builder

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
FROM gcr.io/distroless/base-debian12

# Copy the binary and config files from the builder
COPY --from=builder /saltybytes-api /saltybytes-api
COPY --from=builder /app/configs /configs

# Expose the API port
EXPOSE 8080

# Command to run the application
CMD ["/saltybytes-api"]
