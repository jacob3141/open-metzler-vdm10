FROM golang:1.26-alpine AS build
WORKDIR /src
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags='-s -w' -o /out/doorbell-hub . \
 && mkdir -p /out/data

FROM scratch
COPY --from=build /out/doorbell-hub /doorbell-hub
COPY --from=build --chown=65532:65532 /out/data /data
USER 65532:65532
VOLUME /data
EXPOSE 8088
ENTRYPOINT ["/doorbell-hub"]
