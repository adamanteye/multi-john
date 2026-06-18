FROM golang AS multijohn-builder
WORKDIR /go/src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o multijohn

FROM debian:bookworm-slim AS john-builder
ARG JTR_REPO=https://github.com/openwall/john.git
ARG JTR_BRANCH=bleeding-jumbo

RUN apt-get update && apt-get install -y --no-install-recommends \
  ca-certificates \
  git \
  build-essential \
  libssl-dev \
  zlib1g-dev \
  pkg-config \
  libgmp-dev \
  libpcap-dev \
  libbz2-dev \
  && rm -rf /var/lib/apt/lists/*

RUN git clone --depth 1 --branch "${JTR_BRANCH}" "${JTR_REPO}" /jtr \
  && cd /jtr/src \
  && ./configure \
  && make -s clean \
  && make -sj"$(nproc)"

FROM debian:bookworm-slim
RUN apt-get update && apt-get install -y --no-install-recommends \
  ca-certificates \
  libssl3 \
  zlib1g \
  libgmp10 \
  libpcap0.8 \
  libbz2-1.0 \
  libgomp1 \
  && rm -rf /var/lib/apt/lists/*

COPY --from=john-builder /jtr /jtr
ENV JOHN_PATH=/jtr/run/john
WORKDIR /go/src
COPY --from=multijohn-builder /go/src .
CMD ["./multijohn"]
