FROM golang:1.23-alpine AS builder

RUN apk add --no-cache git ca-certificates

WORKDIR /build
COPY go.mod go.sum ./
RUN go mod download

COPY . .

RUN CGO_ENABLED=0 go build -ldflags="-s -w" -o /build/docker-dhcp-ipam-plugin .

FROM alpine:3.20 AS runner

RUN apk add --no-cache ca-certificates iptables

COPY --from=builder /build/docker-dhcp-ipam-plugin /usr/bin/docker-dhcp-ipam-plugin

ENTRYPOINT ["/usr/bin/docker-dhcp-ipam-plugin"]
