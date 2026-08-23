FROM golang:1.26.3-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
COPY third_party/go-sqlcipher/go.mod third_party/go-sqlcipher/go.mod
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /beresta-server ./cmd/beresta-server

FROM scratch
COPY --from=build /beresta-server /beresta-server
COPY build/server/container-config.yaml /etc/beresta/config.yaml
VOLUME ["/data"]
EXPOSE 8446
ENTRYPOINT ["/beresta-server", "--config", "/etc/beresta/config.yaml", "--data", "/data"]
