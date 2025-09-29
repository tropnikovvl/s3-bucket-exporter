FROM golang:1.25.1 AS builder

WORKDIR /build

COPY . .

RUN CGO_ENABLED=0 go build -a -o s3-bucket-exporter ./cmd/s3-bucket-exporter

FROM gcr.io/distroless/static-debian12:nonroot

COPY --from=builder /build/s3-bucket-exporter /

ENTRYPOINT ["/s3-bucket-exporter"]
