# syntax=docker/dockerfile:1

FROM golang:1.26.6-alpine AS build

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . ./
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/serviceops-api ./cmd/api \
    && CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/bootstrap-super-admin ./cmd/bootstrap-super-admin

FROM alpine:3.22

RUN apk --no-cache add ca-certificates \
    && addgroup -S serviceops \
    && adduser -S -G serviceops serviceops

COPY --from=build /out/serviceops-api /usr/local/bin/serviceops-api
COPY --from=build /out/bootstrap-super-admin /usr/local/bin/bootstrap-super-admin

USER serviceops

EXPOSE 8080

ENTRYPOINT ["/usr/local/bin/serviceops-api"]
