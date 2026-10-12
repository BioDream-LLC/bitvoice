#!/bin/sh
# Builds BitVoice machine images for the AWS and Azure marketplaces from a plain Ubuntu image, using
# the clouds' own command-line tools (aws, az) and the cloud-init file beside this script.
#
#   ./build-images.sh aws   ami-0123456789abcdef0 subnet-0123 sg-0123 https://.../bitvoice-linux-amd64
#   ./build-images.sh azure my-resource-group eastus https://.../bitvoice-linux-amd64
set -eu
here=$(cd "$(dirname "$0")" && pwd)
userdata() {
  svc=$(base64 < "$here/bitvoice.service" | tr -d '\n')
  sed -e "s|BITVOICE_SERVICE_B64|$svc|" -e "s|\${BITVOICE_URL:-https://example.invalid/bitvoice-linux-amd64}|$1|" "$here/cloud-init.yaml"
}
case "${1:-}" in
aws)
  base=$2 subnet=$3 sg=$4 url=$5
  userdata "$url" > /tmp/bitvoice-userdata.yaml
  id=$(aws ec2 run-instances --image-id "$base" --instance-type t3.small --subnet-id "$subnet" --security-group-ids "$sg" \
    --user-data file:///tmp/bitvoice-userdata.yaml --query 'Instances[0].InstanceId' --output text)
  echo "builder $id: waiting for it to install BitVoice"
  aws ec2 wait instance-status-ok --instance-ids "$id"
  sleep 60
  aws ec2 stop-instances --instance-ids "$id" >/dev/null
  aws ec2 wait instance-stopped --instance-ids "$id"
  ami=$(aws ec2 create-image --instance-id "$id" --name "bitvoice-$(date +%Y%m%d%H%M)" --description "BitVoice phone system by BioDream LLC" --query ImageId --output text)
  aws ec2 wait image-available --image-ids "$ami"
  aws ec2 terminate-instances --instance-ids "$id" >/dev/null
  echo "image $ami"
  ;;
azure)
  rg=$2 region=$3 url=$4
  userdata "$url" > /tmp/bitvoice-userdata.yaml
  az vm create -g "$rg" -n bitvoice-builder -l "$region" --image Ubuntu2204 --size Standard_B2s \
    --custom-data /tmp/bitvoice-userdata.yaml --generate-ssh-keys --output none
  sleep 180
  az vm run-command invoke -g "$rg" -n bitvoice-builder --command-id RunShellScript --scripts "sudo waagent -deprovision -force" --output none
  az vm deallocate -g "$rg" -n bitvoice-builder
  az vm generalize -g "$rg" -n bitvoice-builder
  az image create -g "$rg" -n "bitvoice-$(date +%Y%m%d%H%M)" --source bitvoice-builder --output table
  az vm delete -g "$rg" -n bitvoice-builder --yes
  ;;
*)
  echo "usage: $0 aws <base ami> <subnet> <security group> <release url> | azure <resource group> <region> <release url>" >&2
  exit 2
  ;;
esac
