# This is just slim image to load prebuilt binary and test it

# stage: builder
# FROM docker.infra.babelforce.com/golang/go-build:1.20-alpine as builder
# FROM debian
# WORKDIR /app
# ENV CGO_ENABLED=0

# # upgrade distro and install some tools
# RUN apt-get update && apt-get upgrade --yes && \
#   apt-get install --yes \
#   gettext-base \
#   wget \
#   curl \
#   sngrep


FROM alpine:latest
WORKDIR /app
ENV CGO_ENABLED=0

# Install only essential runtime tools
# RUN apk add --no-cache sngrep

COPY diagox .
COPY example-configs/diagox_example.yaml ./diagox.yaml
# RUN go install github.com/go-delve/delve/cmd/dlv@latest
# RUN go env && go mod download
# ENTRYPOINT [ "./diagox" ]
CMD [ "./diagox" ]


# ENTRYPOINT ["dlv", "--listen=${dlv_port}", "--headless=true", "--api-version=2", "--accept-multiclient", "exec", "./main"]
# ENTRYPOINT [ "dlv", "debug", ".", "--"]
# CMD ["-server"]

