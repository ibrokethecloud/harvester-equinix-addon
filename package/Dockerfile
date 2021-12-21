FROM alpine:3.14.2
ARG ARCH
ENV ARCH=$ARCH
COPY bin/harvester-equinix-addon /usr/bin/harvester-equinix-addon
USER root
CMD ["harvester-equinix-addon"]