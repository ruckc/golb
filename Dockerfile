FROM docker.io/library/golang:1.24.6-bookworm AS build

WORKDIR /app

COPY . /app

RUN CGO_ENABLED=0 go build -trimpath -o golb cmd/golb/main.go

FROM gcr.io/distroless/static-debian12

COPY --from=build /app/golb/main /golb

EXPOSE 8080
ENTRYPOINT ["/golb"]

