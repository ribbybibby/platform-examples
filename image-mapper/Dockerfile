FROM cgr.dev/chainguard/go:latest-dev AS builder

WORKDIR /app

# Add helm into the image 
COPY --from=cgr.dev/chainguard/static:latest / /base-chroot
RUN apk add --no-commit-hooks --no-cache --root /base-chroot helm

COPY go.mod go.sum ./
RUN go mod download

COPY main.go main.go
COPY internal internal
COPY cmd cmd

RUN CGO_ENABLED=0 go build -o image-mapper .

FROM cgr.dev/chainguard/static:latest

COPY --from=builder /base-chroot /
COPY --from=builder /app/image-mapper /usr/local/bin/image-mapper

ENTRYPOINT ["/usr/local/bin/image-mapper"]
