FROM docker.io/library/golang:1.24.3-bookworm AS build

WORKDIR /app

COPY . /app

RUN CGO_ENABLED=0 go build -trimpath -o golb cmd/golb/main.go

FROM gcr.io/distroless/static-debian12

COPY --from=build /app/golb /golb

EXPOSE 8080
ENTRYPOINT ["/golb"]

