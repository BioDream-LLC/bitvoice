#!/bin/sh
# Builds a BitVoice machine image for AWS or Azure from a plain Ubuntu image, using the clouds' own
# command-line tools (aws, az) and the cloud-init file beside this script.
#
#   ./build-images.sh aws   <base ami> <subnet> <security group> <version>      e.g. ami-0123 subnet-0123 sg-0123 v0.1.8
#   ./build-images.sh azure <resource group> <region> <version>
#
# The builder installs the release, then empties BitVoice's data and cloud-init's state and powers itself
# off, so every machine made from the image starts as a new server: its own setup code, certificate and
# database. BITVOICE_AWS_TAGS adds tags to the builder and the image (Key=Project,Value=x Key=Owner,Value=y).
# The subnet must give the builder a way out to GitHub (a public address or NAT).
set -eu
here=$(cd "$(dirname "$0")" && pwd)
userdata() { # version
  sed "s|BITVOICE_VERSION_VALUE|$1|" "$here/cloud-init.yaml"
  # Only for an image: leave nothing of this machine behind, then stop.
  cat <<'SNIP'
  - [ sh, -c, 'systemctl is-active --quiet bitvoice || exit 1; systemctl stop bitvoice; rm -rf /var/lib/bitvoice/* /root/bitvoice-install.sh' ]
  - [ sh, -c, 'cloud-init clean --logs --seed; rm -f /etc/ssh/ssh_host_*; truncate -s 0 /etc/machine-id; poweroff' ]
SNIP
}
case "${1:-}" in
aws)
  [ $# -eq 5 ] || { echo "usage: $0 aws <base ami> <subnet> <security group> <version>" >&2; exit 2; }
  base=$2 subnet=$3 sg=$4 version=$5
  tags=${BITVOICE_AWS_TAGS:-}
  ud=$(mktemp) && trap 'rm -f "$ud"' EXIT
  userdata "$version" > "$ud"
  ts="ResourceType=instance,Tags=[{Key=Name,Value=bitvoice-image-builder}]"
  [ -n "$tags" ] && ts="ResourceType=instance,Tags=[{Key=Name,Value=bitvoice-image-builder},$(echo "$tags" | sed 's/Key=\([^,]*\),Value=\([^ ]*\)/{Key=\1,Value=\2}/g; s/ /,/g')]"
  id=$(aws ec2 run-instances --image-id "$base" --instance-type t3.small --subnet-id "$subnet" --security-group-ids "$sg" \
    --tag-specifications "$ts" --user-data "file://$ud" --query 'Instances[0].InstanceId' --output text)
  echo "builder $id: installing BitVoice; it powers itself off when done"
  i=0
  until [ "$(aws ec2 describe-instances --instance-ids "$id" --query 'Reservations[0].Instances[0].State.Name' --output text)" = stopped ]; do
    i=$((i + 1)); [ $i -le 120 ] || { echo "builder $id did not finish in 20 minutes: look at its console output" >&2; exit 1; }
    sleep 10
  done
  ami=$(aws ec2 create-image --instance-id "$id" --name "bitvoice-$version-$(date +%Y%m%d%H%M)" \
    --description "BitVoice $version phone system by BioDream LLC" --query ImageId --output text)
  [ -n "$tags" ] && aws ec2 create-tags --resources "$ami" --tags $tags
  aws ec2 wait image-available --image-ids "$ami"
  aws ec2 terminate-instances --instance-ids "$id" >/dev/null
  echo "image $ami"
  ;;
azure)
  [ $# -eq 4 ] || { echo "usage: $0 azure <resource group> <region> <version>" >&2; exit 2; }
  rg=$2 region=$3 version=$4
  ud=$(mktemp) && trap 'rm -f "$ud"' EXIT
  sed "s|BITVOICE_VERSION_VALUE|$version|" "$here/cloud-init.yaml" > "$ud"
  az vm create -g "$rg" -n bitvoice-builder -l "$region" --image Ubuntu2404 --size Standard_B2s \
    --custom-data "$ud" --generate-ssh-keys --output none
  az vm run-command invoke -g "$rg" -n bitvoice-builder --command-id RunShellScript --scripts \
    "cloud-init status --wait; systemctl is-active --quiet bitvoice || exit 1; systemctl stop bitvoice; rm -rf /var/lib/bitvoice/* /root/bitvoice-install.sh; waagent -deprovision -force" --output none
  az vm deallocate -g "$rg" -n bitvoice-builder
  az vm generalize -g "$rg" -n bitvoice-builder
  az image create -g "$rg" -n "bitvoice-$version-$(date +%Y%m%d%H%M)" --source bitvoice-builder --output table
  az vm delete -g "$rg" -n bitvoice-builder --yes
  ;;
*)
  echo "usage: $0 aws <base ami> <subnet> <security group> <version> | azure <resource group> <region> <version>" >&2
  exit 2
  ;;
esac
