# syntax=docker/dockerfile:1
# IPAM service image: builds the Vue remote, embeds it (-tags ui), and produces
# a slim runtime carrying ipamsvc. Build context is the repo root so the
# module's replace directives (../.. and sibling services) resolve. The runtime
# grants ipamsvc CAP_NET_RAW so it can open the raw ICMP socket used by subnet
# scans without running as root.

FROM node:22-alpine AS ui
WORKDIR /ui
COPY services/ipam/ui/package.json services/ipam/ui/package-lock.json* ./
RUN npm ci --no-audit --no-fund || npm install --no-audit --no-fund
COPY services/ipam/ui/ ./
RUN npm run build

FROM golang:1.26-alpine AS build
RUN apk add --no-cache git ca-certificates
WORKDIR /src
COPY . .
COPY --from=ui /ui/dist ./services/ipam/ui/dist
WORKDIR /src/services/ipam
ENV CGO_ENABLED=0 GOFLAGS=-buildvcs=false
RUN go build -tags "ui" -o /out/ipamsvc ./cmd/ipamsvc

FROM alpine:3.20
RUN apk add --no-cache ca-certificates postgresql-client libcap && adduser -D -u 10001 app
COPY --from=build /out/ipamsvc /usr/local/bin/
# CAP_NET_RAW lets the unprivileged app user send ICMP echo for discovery scans.
RUN setcap cap_net_raw+ep /usr/local/bin/ipamsvc
COPY services/ipam/deploy /app/deploy
WORKDIR /app
USER app
ENTRYPOINT ["ipamsvc"]
CMD ["-config", "deploy/container.yaml"]
