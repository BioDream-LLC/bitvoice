# The lab image: a BitVoice built for Linux (run.sh builds it as ./bitvoice) on Alpine, which brings
# the ip command BitVoice uses to take the shared address, and poppler, which draws PDF pages for
# sending faxes.
FROM alpine:3.20
RUN apk add --no-cache poppler-utils
COPY bitvoice /usr/local/bin/bitvoice
ENTRYPOINT ["/usr/local/bin/bitvoice"]
