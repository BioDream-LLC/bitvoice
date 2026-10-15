#!/bin/sh
# Publishes dist-site (make site) to bitvoice.org: the S3 bucket behind CloudFront, each file type
# with its Content-Type named (S3 does not guess one), then a CloudFront invalidation.
#
# Usage: deploy/site.sh [--no-invalidate]. Needs the AWS CLI with the account that owns the bucket.
set -eu

BUCKET="s3://bitvoice-website-893675906148"
DISTRIBUTION="E38RL5MQ4CANDS"
DIR="dist-site"

[ -d "$DIR" ] || { echo "no $DIR: run make site first" >&2; exit 1; }

missing=$(find "$DIR" -type f ! -name "*.html" ! -name "*.svg" ! -name "*.webp" ! -name "*.png" \
  ! -name "*.pdf" ! -name "*.xml" ! -name "*.txt" ! -name "*.css")
if [ -n "$missing" ]; then
  echo "no Content-Type rule for these; add them to this script:" >&2
  echo "$missing" >&2
  exit 1
fi

cd "$DIR"
# HTML is checked often, as it changes with every release; the rest is kept a day.
aws s3 sync . "$BUCKET" --delete --exclude "*" --include "*.html" \
  --content-type "text/html; charset=utf-8" --cache-control "public, max-age=300, must-revalidate" --only-show-errors
for spec in "*.svg:image/svg+xml" "*.webp:image/webp" "*.png:image/png" "*.pdf:application/pdf" \
  "*.xml:application/xml; charset=utf-8" "*.txt:text/plain; charset=utf-8" "*.css:text/css; charset=utf-8"; do
  pattern=${spec%%:*}
  type=${spec#*:}
  [ -n "$(find . -name "$pattern" -type f | head -1)" ] || continue
  aws s3 sync . "$BUCKET" --exclude "*" --include "$pattern" \
    --content-type "$type" --cache-control "public, max-age=86400" --only-show-errors
done
# The --delete above only removed stale HTML; remove other files no longer in the build.
aws s3 sync . "$BUCKET" --delete --exclude "*.html" --size-only --only-show-errors --dryrun \
  | sed -n 's/^(dryrun) delete: //p' | while read -r obj; do aws s3 rm "$obj" --only-show-errors; done
cd - >/dev/null

if [ "${1:-}" != "--no-invalidate" ]; then
  aws cloudfront create-invalidation --distribution-id "$DISTRIBUTION" --paths "/*" \
    --query 'Invalidation.Id' --output text
fi
echo done
