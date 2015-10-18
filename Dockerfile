FROM busybox

ENTRYPOINT ["/resolvable"]

COPY ./resolvable /resolvable
COPY ./config /config
