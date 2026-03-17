FROM gcr.io/distroless/static:nonroot

ARG TARGETARCH

COPY bin/imap-server-${TARGETARCH} /imap-server

USER nonroot:nonroot
EXPOSE 143

ENTRYPOINT ["/imap-server"]
