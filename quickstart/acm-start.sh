#!/bin/bash
# Copyright 2022.
# 
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
# 
#     http://www.apache.org/licenses/LICENSE-2.0
# 
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.


if [ "${S3_CREDS}" == "" ]; then
  printf "**WARNING** No S3_CREDS found, export it to avoid manual entry\n"
  printf "Enter the path and file name for the S3 AWS credential\n"
  read S3_CREDS

  if [ ! -f "${S3_CREDS}" ]; then
    printf "Could not find S3_CREDS file: ${S3_CREDS}"
    exit 1
  fi
fi

if [ "${BUCKET_NAME}" == "" ]; then
  printf "\n**WARNING** No BUCKET_NAME found, export it to avoid manual entry\n"
  printf "Enter the S3 bucket name\n"
  read BUCKET_NAME
  if [ ${BUCKET_NAME} == "" ]; then
    echo "No BUCKET_NAME provided"
    exit 1
  fi
fi

if [ "${BUCKET_REGION}" == "" ]; then
  printf "\n**WARNING** No BUCKET_REGION found, export it to avoid manual entry\n"
  printf "Enter the region that constains the S3 bucket\n"
  read BUCKET_REGION
  if [ ${BUCKET_REGION} == "" ]; then
    echo "No BUCKET_REGION provided"
    exit 1
  fi
fi
printf "S3 credentials: ${S3_CREDS}\n"
printf "Bucket name   : ${BUCKET_NAME}\n"
printf "Bucket region : ${BUCKET_REGION}\nTesting bucket\n"

CMD="AWS_SHARED_CREDENTIALS_FILE=${S3_CREDS} aws --region ${BUCKET_REGION} s3 ls ${BUCKET_NAME}"
which aws
if [ $? -eq 0 ]; then
  eval ${CMD}
  if [ $? -ne 0 ]; then
    printf "Bucket with details listed above does not exist\n"
    exit 1
  fi
else
  printf "**WARNING** AWS CLI is not present, so S3_CREDS can not be validated, continuing to test URL\n"
fi

printf "Testing the s3 URL: https://${BUCKET_NAME}.s3.${BUCKET_REGION}.amazonaws.com"
CMD="curl https://${BUCKET_NAME}.s3.${BUCKET_REGION}.amazonaws.com 2>&1 | grep \"Access Denied\""
eval ${CMD}
if [ $? -ne 0 ]; then
  printf "Bucket with details listed above does not exist\n"
  exit 1
fi

if [ "${CLUSTER}" == "" ]; then
  printf "\n**WARNING** No CLUSTER found, export it to avoid manual entry\n"
  printf "Enter the CLUSTER name to deploy the addon\n"
  read CLUSTER
  if [ ${CLUSTER} == "" ]; then
    echo "No CLUSTER provided"
    exit 1
  fi
fi


oc project ${CLUSTER}
if [ $? -ne 0 ]; then
  echo "${CLUSTER} is not valid, please provide a valid cluster name"
  exit 1
fi

# install hypershift addon
echo "start to install hypershift addon on the local cluster"
oc apply -f - <<EOF
apiVersion: addon.open-cluster-management.io/v1alpha1
kind: ManagedClusterAddOn
metadata:
  name: hypershift-addon
  namespace: ${CLUSTER}
spec:
  installNamespace: open-cluster-management-agent-addon
EOF

oc create secret generic hypershift-operator-oidc-provider-s3-credentials --from-file=credentials=${S3_CREDS} --from-literal=bucket=${BUCKET_NAME} --from-literal=region=${BUCKET_REGION} -n local-cluster

echo "wait for managed cluster addon hypershift addon to be available ..."
oc wait --for=condition=Available managedclusteraddon/hypershift-addon -n local-cluster --timeout=600s
if [ $? -ne 0 ]
then
  echo "hypershift addon installation failed"
  exit 1
else
  echo "hypershift addon installed successfully, now you can provision a hosted control plane cluster by HypershiftDeployment"
fi
