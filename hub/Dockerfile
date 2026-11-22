FROM golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags='-s -w' -o /out/doorbell-hub . \
 && mkdir -p /out/data

FROM scratch
COPY --from=build /out/doorbell-hub /doorbell-hub
COPY --from=build --chown=65532:65532 /out/data /data
# The Android app, built by CI (see .github/workflows/docker.yml). Empty in a local build.
COPY app/ /app/
USER 65532:65532
VOLUME /data
EXPOSE 80 5353/udp
ENTRYPOINT ["/doorbell-hub"]
