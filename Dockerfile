##################################
# Stage 0: Build frontend module
##################################

FROM node:20-alpine AS frontend-builder

RUN npm install -g pnpm@9

WORKDIR /frontend
COPY go-tangra-ipam/frontend/package.json go-tangra-ipam/frontend/pnpm-lock.yaml* ./
RUN pnpm install --frozen-lockfile || pnpm install
COPY go-tangra-ipam/frontend/ .
RUN pnpm build

##################################
# Stage 1: Build Go executable
##################################

FROM golang:1.23-alpine AS builder

ARG APP_VERSION=1.0.0

# Enable toolchain auto-download for newer Go versions
ENV GOTOOLCHAIN=auto

# Install build dependencies
RUN apk add --no-cache git make curl

# Install buf for proto descriptor generation
RUN curl -sSL "https://github.com/bufbuild/buf/releases/latest/download/buf-$(uname -s)-$(uname -m)" -o /usr/local/bin/buf && \
    chmod +x /usr/local/bin/buf

# Set working directory
WORKDIR /src

# Copy go mod files first for better caching
COPY go-tangra-ipam/go.mod go-tangra-ipam/go.sum ./

# Copy go-tangra-common (local dependency)
COPY go-tangra-common/ /go-tangra-common/
RUN go mod edit -replace github.com/go-tangra/go-tangra-common=/go-tangra-common

RUN go mod download

# Copy the entire source code
COPY go-tangra-ipam/ .

# Regenerate proto descriptor (ensures embedded descriptor.bin is always up to date)
RUN buf build -o cmd/server/assets/descriptor.bin

# Build the server
RUN CGO_ENABLED=0 \
    GOOS=linux \
    GOARCH=amd64 \
    go build -ldflags "-X main.version=${APP_VERSION} -s -w" \
    -o /src/bin/ipam-server \
    ./cmd/server

##################################
# Stage 2: Create runtime image
##################################

FROM alpine:3.20

ARG APP_VERSION=1.0.0

# Install runtime dependencies
RUN apk --no-cache add ca-certificates tzdata libcap

# Set timezone
ENV TZ=UTC

# Set working directory
WORKDIR /app

# Copy executable from builder
COPY --from=builder /src/bin/ipam-server /app/bin/ipam-server

# Copy configuration files
COPY --from=builder /src/configs/ /app/configs/

# Copy frontend assets from frontend builder
COPY --from=frontend-builder /frontend/dist /app/frontend-dist

# Create non-root user
RUN addgroup -g 1000 ipam && \
    adduser -D -u 1000 -G ipam ipam && \
    chown -R ipam:ipam /app

# Grant NET_RAW capability for ICMP ping scanning
# NOTE: must run AFTER chown, as chown strips file capabilities
RUN setcap cap_net_raw+ep /app/bin/ipam-server

# Switch to non-root user
USER ipam:ipam

# Expose gRPC and HTTP ports
EXPOSE 9400 9401

# Set default command
CMD ["/app/bin/ipam-server", "-c", "/app/configs"]

# Labels
LABEL org.opencontainers.image.title="IPAM Service" \
      org.opencontainers.image.description="IP Address Management Service" \
      org.opencontainers.image.version="${APP_VERSION}"
