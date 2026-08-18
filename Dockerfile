# Minify client side assets (JavaScript)
FROM node:24.19.0-bookworm-slim@sha256:3638d9a6fe4030bd716be989438248074489337ba3275657f93595428be4fc03 AS build-js

WORKDIR /build
COPY package.json yarn.lock ./
RUN corepack enable && yarn install --frozen-lockfile --non-interactive
COPY . .
RUN yarn build


# Build Golang binary
FROM golang:1.25.13-bookworm@sha256:e401dae1bf814e29204a8cb7915682e1780951e609ca0dd8865ee1937f510c48 AS build-golang

WORKDIR /go/src/github.com/Vesperis-group/gophishfr
COPY . .
# No `go get`: dependencies come from the committed go.mod/go.sum only, and
# their checksums are verified before anything is compiled.
RUN go mod download && go mod verify
RUN go build -v -o gophishfr .


# Runtime container
FROM debian:stable-slim

# Keep the runtime directory stable because config.json uses relative database
# and log paths, and existing deployments may mount /opt/gophish.
RUN useradd -m -d /opt/gophish -s /bin/bash app

RUN apt-get update && \
	apt-get install --no-install-recommends -y jq libcap2-bin ca-certificates && \
	apt-get clean && \
	rm -rf /var/lib/apt/lists/* /tmp/* /var/tmp/*

WORKDIR /opt/gophish
COPY --from=build-golang /go/src/github.com/Vesperis-group/gophishfr/ ./
COPY --from=build-js /build/static/js/dist/ ./static/js/dist/
COPY --from=build-js /build/static/css/dist/ ./static/css/dist/
COPY --from=build-golang /go/src/github.com/Vesperis-group/gophishfr/config.json ./
RUN chown app. config.json

RUN setcap 'cap_net_bind_service=+ep' /opt/gophish/gophishfr

USER app
RUN sed -i 's/127.0.0.1/0.0.0.0/g' config.json
RUN touch config.json.tmp

EXPOSE 3333 8080 8443 80

CMD ["./docker/run.sh"]
