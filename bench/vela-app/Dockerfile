# Vela + Go version of the benchmark tracker. Build from the repository root:
#   docker build -f bench/vela-app/Dockerfile -t bench-vela .
FROM golang:1.27 AS build
RUN go install github.com/evanw/esbuild/cmd/esbuild@v0.28.2
WORKDIR /src
COPY . .
ENV GOWORK=off
RUN ESBUILD=/go/bin/esbuild bench/vela-app/build.sh \
 && cd bench && go build -o /out/seed ./seed

FROM debian:stable-slim
COPY --from=build /src/bench/vela-app/bin/server /app/vela-app/bin/server
COPY --from=build /src/bench/vela-app/static /app/vela-app/static
COPY --from=build /out/seed /app/bin/seed
COPY bench/shared /app/shared
WORKDIR /app
ENV APP_DIR=/app/vela-app PORT=8081 STORAGE_DIR=/app/storage
EXPOSE 8081
CMD ["/app/vela-app/bin/server"]
