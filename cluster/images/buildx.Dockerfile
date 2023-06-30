FROM alpine:3.18.2

ARG BINARY
ARG TARGETPLATFORM

RUN apk add --no-cache ca-certificates
RUN apk add --no-cache tzdata

COPY ${TARGETPLATFORM}/${BINARY} /bin/${BINARY}
