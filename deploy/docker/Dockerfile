FROM alpine:3.22

RUN apk add --no-cache ca-certificates tzdata \
    && addgroup -S workflow \
    && adduser -S -G workflow -h /var/lib/workflow workflow \
    && install -d -o workflow -g workflow -m 0700 /var/lib/workflow

WORKDIR /opt/workflow
COPY --chown=workflow:workflow workflow-server /opt/workflow/workflow-server
COPY --chown=workflow:workflow web /opt/workflow/web

USER workflow
ENV WORKFLOW_MODE=server \
    WORKFLOW_ADDR=0.0.0.0:55080 \
    WORKFLOW_DATA_DIR=/var/lib/workflow \
    WORKFLOW_WEB_DIR=/opt/workflow/web
EXPOSE 55080
VOLUME ["/var/lib/workflow"]
HEALTHCHECK --interval=10s --timeout=3s --start-period=5s --retries=3 \
  CMD wget -q -O /dev/null http://127.0.0.1:55080/readyz || exit 1

ENTRYPOINT ["/opt/workflow/workflow-server"]
