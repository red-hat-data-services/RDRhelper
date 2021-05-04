#!/bin/bash

set -ex

seq 20 | xargs -P 5 -I {} sh -c "export pvc_name=\"test{}\"; j2 $(dirname "$0")/pvc.yaml.j2 | oc delete -f -"
