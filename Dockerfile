FROM debian:bookworm-slim

RUN apt-get update \
	&& apt-get install -y --no-install-recommends ca-certificates curl bash openssl \
	&& rm -rf /var/lib/apt/lists/*

RUN groupadd -g 1000 firmware \
	&& useradd -m -u 1000 -g firmware -d /home/firmware -s /usr/sbin/nologin firmware

WORKDIR /home/firmware

# Binaries, entrypoint and default device profiles.
COPY firmware-updater /usr/local/bin/firmware-updater
COPY secret-cli /usr/local/bin/secret-cli
COPY docker-entrypoint.sh /usr/local/bin/docker-entrypoint.sh
COPY --chown=firmware:firmware device-profiles/ /home/firmware/device-profiles/

RUN chmod +x /usr/local/bin/docker-entrypoint.sh \
	&& chown -R firmware:firmware /home/firmware

USER firmware

# ---------------------------------------------------------------------------
# Environment variables (viper prefix FIRMWARE_UPDATER_, '-' mapped to '_')
# ---------------------------------------------------------------------------
ENV FIRMWARE_UPDATER_HOST=0.0.0.0 \
	FIRMWARE_UPDATER_PORT=8080 \
	FIRMWARE_UPDATER_DATABASE_URL="file:/home/firmware/data.db?cache=shared&_fk=1" \
	FIRMWARE_UPDATER_SECRETS_FILE=/home/firmware/secrets.json \
	FIRMWARE_UPDATER_DEVICE_PROFILES_DIR=/home/firmware/device-profiles \
	FIRMWARE_UPDATER_DEBUG=false

# MASTER_KEY unlocks the encrypted secrets store. Dockerfile ENV does NOT run
# command substitution, so if left empty the entrypoint generates an ephemeral
# key at startup. Provide your own to persist a store across runs:
#   -e MASTER_KEY="$(openssl rand -hex 32)"
ENV MASTER_KEY=""

# Default secret seeded into the store by the entrypoint.
ENV SECRET_ID=x9000-bmc \
	SECRET_USERNAME=root \
	SECRET_PASSWORD=initial0

EXPOSE 8080

ENTRYPOINT ["/usr/local/bin/docker-entrypoint.sh"]
CMD ["serve"]