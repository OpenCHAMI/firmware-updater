FROM alpine:latest

RUN apk add --no-cache ca-certificates curl bash git

RUN addgroup -g 1000 firmware && adduser -D -u 1000 -G firmware firmware

WORKDIR /home/firmware

COPY firmware-updater /usr/local/bin/firmware-updater

RUN chown -R firmware:firmware /home/firmware

USER firmware

ENTRYPOINT ["/usr/local/bin/firmware-updater"]
