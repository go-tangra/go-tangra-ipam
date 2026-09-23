# syntax=docker/dockerfile:1
# IPAM service image: builds the Vue remote, embeds it (-tags ui), and produces
# a slim runtime carrying ipamsvc. Build context is the repo root so the
# module's replace directives (../.. and sibling services) resolve. The runtime
# grants ipamsvc CAP_NET_RAW so it can open the raw ICMP socket used by subnet
# scans without running as root.

FROM node:22-alpine AS ui
# The front-ends form one npm workspace (root package-lock.json) with the shared
# kit at ui/kit; install the workspace, build the kit, then this front-end.
WORKDIR /w
COPY package.json package-lock.json .npmrc ./
COPY ui/kit/package.json ui/kit/
COPY services/gateway/shell/package.json services/gateway/shell/
COPY services/auth/console/package.json services/auth/console/
COPY services/asset/ui/package.json services/asset/ui/
COPY services/inventory/ui/package.json services/inventory/ui/
COPY services/ipam/ui/package.json services/ipam/ui/
COPY services/paperless/ui/package.json services/paperless/ui/
COPY services/deployer/ui/package.json services/deployer/ui/
COPY services/lcm/ui/package.json services/lcm/ui/
COPY services/notification/ui/package.json services/notification/ui/
COPY services/warden/ui/package.json services/warden/ui/
RUN npm ci --no-audit --no-fund
COPY ui/ ./ui/
RUN npm run -w ui/kit build
COPY services/ipam/ui/ ./services/ipam/ui/
RUN npm run -w services/ipam/ui build

FROM golang:1.26-alpine AS build
RUN apk add --no-cache git ca-certificates
WORKDIR /src
COPY . .
COPY --from=ui /w/services/ipam/ui/dist ./services/ipam/ui/dist
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
