# 1. CRITICAL: Add --platform=$BUILDPLATFORM here
FROM --platform=$BUILDPLATFORM golang:1.26.1-alpine AS build

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .


ARG TARGETOS
ARG TARGETARCH


RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} go build -trimpath -ldflags="-s -w" -o /out/fbs ./cmd/server

FROM alpine:3.22

RUN addgroup -S fbs \
	&& adduser -S -G fbs fbs \
	&& mkdir -p /var/lib/fbs/data \
	&& chown -R fbs:fbs /var/lib/fbs

COPY --from=build /out/fbs /usr/local/bin/fbs

ENV FBS_HTTP_ADDR=0.0.0.0:9000 \
	FBS_DB_PATH=/var/lib/fbs/fbs.db \
	FBS_DATA_DIR=/var/lib/fbs/data

USER fbs
VOLUME ["/var/lib/fbs"]
EXPOSE 9000

ENTRYPOINT ["fbs"]