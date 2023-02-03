# hypershift-addon-operator canary test

## prerequisite

Ensure your kubeconfig context or `KUBECONFIG` environment variable is pointing at the OCP cluster that you want to run the test against.

## build the canary test image

```
cd hypershift-addon-operator/
make docker-build-canary 
```

## run the canary test image locally using docker
You need to have a public route53 hosted zone for external DNS and public S3 bucket as documented in https://hypershift-docs.netlify.app/getting-started/

Edit and run the following:

```
KUBECONFIG_PATH=/home/foobar/.kube/config
OCP_PULL_SECRET=foobar
BASE_DOMAIN=foo.bar
EXT_DNS_DOMAIN=hypershift.foo.bar
S3_BUCKET_NAME=foobar-aws-hypershift
AWS_ACCESS_KEY_ID=foobar
AWS_SECRET_ACCESS_KEY=foobar
```

Edit the following if necessary or run it as it is:

```
OCP_RELEASE_IMAGE=quay.io/openshift-release-dev/ocp-release:4.12.0-x86_64
HOSTING_CLUSTER_NAME=local-cluster
CLUSTER_NAME_PREFIX=ge-
REGION=us-east-1
RESULTS_DIR=/tmp
```

Run the test:

```
docker run \
  --volume $KUBECONFIG_PATH:/kubeconfig \
  --volume $RESULTS_DIR:/results \
  --env KUBECONFIG=/kubeconfig \
  --env OCP_RELEASE_IMAGE=$OCP_RELEASE_IMAGE \
  --env OCP_PULL_SECRET=$OCP_PULL_SECRET \
  --env HOSTING_CLUSTER_NAME=$HOSTING_CLUSTER_NAME \
  --env CLUSTER_NAME_PREFIX=$CLUSTER_NAME_PREFIX \
  --env REGION=$REGION \
  --env BASE_DOMAIN=$BASE_DOMAIN \
  --env EXT_DNS_DOMAIN=$EXT_DNS_DOMAIN \
  --env S3_BUCKET_NAME=$S3_BUCKET_NAME \
  --env AWS_ACCESS_KEY_ID=$AWS_ACCESS_KEY_ID \
  --env AWS_SECRET_ACCESS_KEY=$AWS_SECRET_ACCESS_KEY \
  quay.io/stolostron/hypershift-addon-operator-canary-test:latest
```

## manual cleanup

Run the following:

```
AWS_CREDS_FILE=foobar
BASE_DOMAIN=foobar
for infraID in `oc get ns | grep staging | cut -d' ' -f1 | cut -d'-' -f3 | uniq`
do
	echo $infraID
	hypershift destroy infra aws --aws-creds ${AWS_CREDS_FILE} --base-domain ${BASE_DOMAIN} --infra-id ${infraID}
	hypershift destroy iam aws --aws-creds ${AWS_CREDS_FILE} --infra-id ${infraID}
done
```
