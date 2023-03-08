FROM golang:1.16-alpine

COPY docker/root/ /

EXPOSE 8182

RUN mkdir /data
RUN mkdir /unzip
RUN mkdir /downloads
RUN mkdir /transfers
RUN mkdir /blackhole

WORKDIR /

ENV PREMIUMIZEARR_CONFIG_DIR_PATH=/data
ENV PREMIUMIZEARR_LOGGING_DIR_PATH=/data

COPY build/static /run/static
COPY build/premiumizearrd /run/
COPY build/static /static

RUN echo ls
RUN chmod +x /run/premiumizearrd
ENTRYPOINT ["/run/premiumizearrd"]