FROM --platform=$BUILDPLATFORM golang:1.27.3-alpine3.23 AS build
ARG TARGETOS
ARG TARGETARCH
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd ./cmd
COPY internal ./internal
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath -o /out/fx-quotes ./cmd/api

FROM alpine:3.23
RUN apk add --no-cache ca-certificates
COPY --from=build /out/fx-quotes /usr/local/bin/fx-quotes
USER 65534:65534
EXPOSE 3000
ENTRYPOINT ["fx-quotes"]
