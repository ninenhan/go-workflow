FROM scratch

COPY . /package

ENTRYPOINT ["/package/bin/workflow-server"]
