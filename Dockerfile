FROM debian:bookworm-slim

RUN apt-get update \
	&& apt-get install -y --no-install-recommends ca-certificates curl bash git \
	&& rm -rf /var/lib/apt/lists/*

RUN groupadd -g 1000 firmware \
	&& useradd -m -u 1000 -g firmware -d /home/firmware -s /usr/sbin/nologin firmware

WORKDIR /home/firmware

COPY firmware-updater /usr/local/bin/firmware-updater

RUN chown -R firmware:firmware /home/firmware

USER firmware

ENTRYPOINT ["/usr/local/bin/firmware-updater"]
