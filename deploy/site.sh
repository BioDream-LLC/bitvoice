#!/bin/sh
# Publishes dist-site (make site) to bitvoice.org: the S3 bucket behind CloudFront, each file type
# with its Content-Type named (S3 does not guess one), then a CloudFront invalidation.
#
# Afterwards it tells search engines which pages changed (IndexNow: Bing, Yandex, Seznam and others),
# by comparing each page with the copy already in the bucket. The key in deploy/indexnow.key is public
# by design: it is served as /KEY.txt to prove the site sent the list.
#
# Usage: deploy/site.sh [--no-invalidate] [--no-indexnow] [--indexnow-all].
# Needs the AWS CLI with the account that owns the bucket.
set -eu

BUCKET="s3://bitvoice-website-893675906148"
DISTRIBUTION="E38RL5MQ4CANDS"
DIR="dist-site"
SITE="https://bitvoice.org"
KEY=$(tr -d ' \n' < deploy/indexnow.key)
INVALIDATE=1 INDEXNOW=1 ALL=0
for arg in "$@"; do
  case $arg in
    --no-invalidate) INVALIDATE=0 ;;
    --no-indexnow) INDEXNOW=0 ;;
    --indexnow-all) ALL=1 ;;
    *) echo "unknown option $arg" >&2; exit 2 ;;
  esac
done

[ -d "$DIR" ] || { echo "no $DIR: run make site first" >&2; exit 1; }

missing=$(find "$DIR" -type f ! -name "*.html" ! -name "*.svg" ! -name "*.webp" ! -name "*.png" \
  ! -name "*.pdf" ! -name "*.xml" ! -name "*.txt" ! -name "*.css" ! -name "*.mp4")
if [ -n "$missing" ]; then
  echo "no Content-Type rule for these; add them to this script:" >&2
  echo "$missing" >&2
  exit 1
fi

printf '%s' "$KEY" > "$DIR/$KEY.txt"

# Pages whose content differs from the bucket's copy (an object's ETag is its MD5 for these uploads).
changed=$(mktemp)
trap 'rm -f "$changed"' EXIT
aws s3api list-objects-v2 --bucket "${BUCKET#s3://}" --output text \
  --query "Contents[?ends_with(Key,'.html')].[Key,ETag]" | tr -d '"' > "$changed.old" || : > "$changed.old"
(cd "$DIR" && find . -name "*.html" ! -name 404.html -type f | sed 's|^\./||') | while read -r f; do
  sum=$(md5 -q "$DIR/$f" 2>/dev/null || md5sum "$DIR/$f" | cut -d' ' -f1)
  if [ "$ALL" = 1 ] || ! grep -q "^$f	$sum\$" "$changed.old"; then
    path=${f%index.html}
    echo "$SITE/$path"
  fi
done > "$changed"
rm -f "$changed.old"

cd "$DIR"
# HTML is checked often, as it changes with every release; the rest is kept a day.
aws s3 sync . "$BUCKET" --delete --exclude "*" --include "*.html" \
  --content-type "text/html; charset=utf-8" --cache-control "public, max-age=300, must-revalidate" --only-show-errors
for spec in "*.svg:image/svg+xml" "*.webp:image/webp" "*.png:image/png" "*.pdf:application/pdf" \
  "*.xml:application/xml; charset=utf-8" "*.txt:text/plain; charset=utf-8" "*.css:text/css; charset=utf-8" "*.mp4:video/mp4"; do
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

if [ "$INVALIDATE" = 1 ]; then
  aws cloudfront create-invalidation --distribution-id "$DISTRIBUTION" --paths "/*" \
    --query 'Invalidation.Id' --output text
fi

n=$(wc -l < "$changed" | tr -d ' ')
if [ "$INDEXNOW" = 1 ] && [ "$n" -gt 0 ]; then
  # IndexNow takes up to 10,000 URLs a request; the site has far fewer. 200 and 202 are both success.
  urls=$(sed 's/.*/"&"/' "$changed" | paste -sd, -)
  code=$(curl -s -o /dev/null -w '%{http_code}' -X POST https://api.indexnow.org/indexnow \
    -H 'Content-Type: application/json; charset=utf-8' \
    -d "{\"host\":\"${SITE#https://}\",\"key\":\"$KEY\",\"keyLocation\":\"$SITE/$KEY.txt\",\"urlList\":[$urls]}")
  case $code in
    200|202) echo "IndexNow: $n changed pages sent" ;;
    *) echo "IndexNow answered $code for $n pages (the site itself is deployed)" >&2 ;;
  esac
fi
echo done
