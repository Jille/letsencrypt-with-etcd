FROM golang AS builder

WORKDIR /builder

ENV CGO_ENABLED=0

COPY go.mod go.sum /builder/
RUN go mod download

COPY *.go /builder/
RUN go build -v -o /letsencrypt-with-etcd

FROM alpine

COPY --from=builder /letsencrypt-with-etcd /bin/letsencrypt-with-etcd

ENTRYPOINT ["/bin/letsencrypt-with-etcd"]
