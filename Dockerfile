FROM alpine:3.2

ENTRYPOINT ["/resolvable"]

COPY ./resolvable /resolvable
COPY ./config /config
