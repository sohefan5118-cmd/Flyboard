# syntax=docker/dockerfile:1
FROM golang:1.24-alpine AS build
WORKDIR /src
COPY go.mod go.sum* ./
RUN go mod download
COPY . .
ARG TARGET=panel
RUN CGO_ENABLED=0 go build -trimpath -ldflags='-s -w' -o /out/app ./cmd/${TARGET}
FROM alpine:3.22
# Root is required only to read Compose bind-mounted 0600 secret files. The image
# contains no shell-exposed service port besides the application itself.
RUN apk add --no-cache postgresql-client
COPY --from=build /out/app /app
ENTRYPOINT ["/app"]
