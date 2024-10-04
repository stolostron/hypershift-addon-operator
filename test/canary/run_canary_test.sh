#!/bin/bash

# This script is run on the AWS hub cluster

# If the cluster is not AWS then exit gracefully and report success
# TODO ACM-3290 to support all platforms
CLOUD_LABEL=$(kubectl get managedcluster local-cluster -o jsonpath='{.metadata.labels.cloud}')
if [ "$CLOUD_LABEL" != "Amazon" ]; then
    echo "Skipping test execution. The local-cluster managedcluster does not have a cloud label with the value: Amazon"
    cp /hypershift-success.xml /results
    exit 0
fi

#########################################
#   POPULATE THESE WITH ENV VARS        #
#   ie: export OCP_RELEASE_IMAG=foobar  #
#########################################
# OCP_RELEASE_IMAGE is the OCP release image used by the hosted cluster and node pool
#export OCP_RELEASE_IMAGE=quay.io/openshift-release-dev/ocp-release:4.12.0-rc.6-x86_64
# OCP_PULL_SECRET is a valid pull secret .dockerconfigjson value for the quay.io/openshift-release-dev repository.
#export OCP_PULL_SECRET=
# HOSTING_CLUSTER_NAME is the target managed cluster where the hosted cluster is created. The hypershift-addon must be enabled in this managed cluster.
#export HOSTING_CLUSTER_NAME=local-cluster
#export REGION=us-east-1
#export BASE_DOMAIN=
# This public hosted zone needs to exist in AWS Route53. Replace with your own
# The hypershift-addon must be enabled with external DNS option
#export EXT_DNS_DOMAIN=
#export S3_BUCKET_NAME=
# The hosted cluster name prefix
#export CLUSTER_NAME_PREFIX=ge-
# The AWS creds
#export AWS_ACCESS_KEY_ID=
#export AWS_SECRET_ACCESS_KEY=
# ENABLE_FOUNDATION_CANARY indicates whether the foundation canary test will be ran along with hypershift canary test. It will be run only when the variable is set to true.
#export ENABLE_FOUNDATION_CANARY=

# Canary test expects a xml result file in a folder.
# Default the result to failed until it's successful.
cp /hypershift-failed.xml /results

if [ -z ${OCP_RELEASE_IMAGE+x} ]; then
  echo "OCP_RELEASE_IMAGE is not defined"
  exit 1
fi

if [ -z ${HUB_OCP_VERSION+x} ]; then
  echo "HUB_OCP_VERSION is not defined"
  exit 1
fi

if [ -z ${UNSUPPORTED_OCP_VERSION+x} ]; then
  echo "UNSUPPORTED_OCP_VERSION is not defined"
  exit 1
fi

if [ -z ${OCP_PULL_SECRET+x} ]; then
  echo "OCP_PULL_SECRET is not defined"
  exit 1
fi

if [ -z ${HOSTING_CLUSTER_NAME+x} ]; then
  echo "HOSTING_CLUSTER_NAME is not defined"
  exit 1
fi

if [ -z ${REGION+x} ]; then
  echo "REGION is not defined"
  exit 1
fi

if [ -z ${BASE_DOMAIN+x} ]; then
  echo "BASE_DOMAIN is not defined"
  exit 1
fi

if [ -z ${EXT_DNS_DOMAIN+x} ]; then
  echo "EXT_DNS_DOMAIN is not defined"
  exit 1
fi

if [ -z ${S3_BUCKET_NAME+x} ]; then
  echo "S3_BUCKET_NAME is not defined"
  exit 1
fi

if [ -z ${CLUSTER_NAME_PREFIX+x} ]; then
  echo "CLUSTER_NAME_PREFIX is not defined"
  exit 1
fi

if [ -z ${AWS_ACCESS_KEY_ID+x} ]; then
  echo "AWS_ACCESS_KEY_ID is not defined"
  exit 1
fi


if [ -z ${AWS_SECRET_ACCESS_KEY+x} ]; then
  echo "AWS_SECRET_ACCESS_KEY is not defined"
  exit 1
fi

# https://stackoverflow.com/questions/16989598/comparing-php-version-numbers-using-bash/24067243#24067243
function version_gt() { test "$(printf '%s\n' "$@" | sort -V | head -n 1)" != "$1"; }
if version_gt $HUB_OCP_VERSION $UNSUPPORTED_OCP_VERSION; then
     echo "Supported Openshift version $HUB_OCP_VERSION is greater than $UNSUPPORTED_OCP_VERSION"
else
    echo "Skipping test execution. HUB_OCP_VERSION: $HUB_OCP_VERSION and UNSUPPORTED_OCP_VERSION: $UNSUPPORTED_OCP_VERSION"
    rm -f /results/hypershift-failed.xml
    cp /hypershift-success.xml /results
    exit 0
fi

# Create AWS credentials file
mkdir ~/.aws
cat <<EOF >~/.aws/credentials
[default]
aws_access_key_id=${AWS_ACCESS_KEY_ID}
aws_secret_access_key=${AWS_SECRET_ACCESS_KEY}
EOF

AWS_CREDS_FILE=~/.aws/credentials

# Create ssh keys
ssh-keygen -t rsa -b 4096 -f ssh-privatekey -q -N ""
if [ $? -ne 0 ]; then
  echo "failed to generate ssh keys"
  exit 1
fi

PRIVATE_KEY=$(base64 ssh-privatekey -w 0)
PUBLIC_KEY=$(base64 ssh-privatekey.pub -w 0)

# CLI variables
# This value can be like "kubectl --kubeconfig my/hub/kubeconfig"
KUBECTL_COMMAND="kubectl"
# This value can be a different file path pointing to the hypershift CLI binary like "/my/dir/hypershift"
HYPERSHIFT_COMMAND="hypershift"

OC_COMMAND="oc"

# Generate the first hosted cluster name
CLUSTER_NAME_1=${CLUSTER_NAME_PREFIX}$(cat /dev/urandom | env LC_ALL=C tr -dc 'a-z0-9' | fold -w 6 | head -n 1)
INFRA_ID_1=$(cat /dev/urandom | env LC_ALL=C tr -dc 'a-z0-9' | fold -w 32 | head -n 1)
CLUSTER_UUID_1=$(uuid)
INFRA_OUTPUT_FILE_1=${CLUSTER_NAME_1}-infraout
IAM_OUTPUT_FILE_1=${CLUSTER_NAME_1}-iam

# Generate the second hosted cluster name
CLUSTER_NAME_2=${CLUSTER_NAME_PREFIX}$(cat /dev/urandom | env LC_ALL=C tr -dc 'a-z0-9' | fold -w 6 | head -n 1)
INFRA_ID_2=$(cat /dev/urandom | env LC_ALL=C tr -dc 'a-z0-9' | fold -w 32 | head -n 1)
CLUSTER_UUID_2=$(uuid)
INFRA_OUTPUT_FILE_2=${CLUSTER_NAME_2}-infraout
IAM_OUTPUT_FILE_2=${CLUSTER_NAME_2}-iam

# Generate the kubeconfig file of the hub cluster
HUB_KUBECONFIG="kubeconfig.hub"
if [ "${ENABLE_FOUNDATION_CANARY}" == "true" ]; then
    ${KUBECTL_COMMAND} config view --flatten --minify > ./${HUB_KUBECONFIG}
fi

cleanupAWSResources() {
    ${HYPERSHIFT_COMMAND} destroy iam aws --aws-creds ${AWS_CREDS_FILE} --infra-id ${INFRA_ID_1}
    ${HYPERSHIFT_COMMAND} destroy infra aws --aws-creds ${AWS_CREDS_FILE} --infra-id ${INFRA_ID_1} --base-domain ${BASE_DOMAIN} --name ${CLUSTER_NAME_1} --region ${REGION}
    ${HYPERSHIFT_COMMAND} destroy iam aws --aws-creds ${AWS_CREDS_FILE} --infra-id ${INFRA_ID_2}
    ${HYPERSHIFT_COMMAND} destroy infra aws --aws-creds ${AWS_CREDS_FILE} --infra-id ${INFRA_ID_2} --base-domain ${BASE_DOMAIN} --name ${CLUSTER_NAME_2} --region ${REGION}
}

# Delete all AWS resources on any exit
trap cleanupAWSResources EXIT

createHostedCluster() {
    clusterName=$1
    infraID=$2
    uuid=$3
    infraOutfile=$4
    iamOutfile=$5

    declare -A vars

    vars[OCP_RELEASE_IMAGE]=${OCP_RELEASE_IMAGE}
    vars[OCP_PULL_SECRET]=${OCP_PULL_SECRET}
    vars[HOSTING_CLUSTER_NAME]=${HOSTING_CLUSTER_NAME}
    vars[REGION]=${REGION}
    vars[BASE_DOMAIN]=${BASE_DOMAIN}
    vars[EXT_DNS_DOMAIN]=${EXT_DNS_DOMAIN}
    vars[CLUSTER_NAME_PREFIX]=g${CLUSTER_NAME_PREFIX}
    vars[CLUSTER_NAME]=${clusterName}
    vars[INFRA_ID]=${infraID}
    vars[CLUSTER_UUID]=${uuid}
    vars[PRIVATE_KEY]=${PRIVATE_KEY}
    vars[PUBLIC_KEY]=${PUBLIC_KEY}

    echo "$(date) ==== Creating AWS infrastructure ===="
    echo "$(date) hypershift create infra aws --aws-creds ${AWS_CREDS_FILE} --base-domain ${vars[BASE_DOMAIN]} --infra-id ${vars[INFRA_ID]} --name ${vars[CLUSTER_NAME]} --region ${vars[REGION]} --output-file ${infraOutfile}"

    # Create AWS infrastructure
    ${HYPERSHIFT_COMMAND} create infra aws --aws-creds ${AWS_CREDS_FILE} --base-domain ${vars[BASE_DOMAIN]} --infra-id ${vars[INFRA_ID]} --name ${vars[CLUSTER_NAME]} --region ${vars[REGION]} --output-file ${infraOutfile}
    if [ $? -ne 0 ]; then
        echo "failed to crete infra"
        exit 1
    fi

    # Set infra resource variables
    vars[MACHINE_CIDR]=$(cat ${infraOutfile} | jq '.machineCIDR' | tr -d '"')
    vars[VPC_ID]=$(cat ${infraOutfile} | jq '.vpcID' | tr -d '"')
    vars[ZONE_NAME]=$(cat ${infraOutfile} | jq '.zones[0] .name' | tr -d '"')
    vars[ZONE_SUBNET_ID]=$(cat ${infraOutfile} | jq '.zones[0] .subnetID' | tr -d '"')
    vars[SECURITY_GROUP_ID]=$(cat ${infraOutfile} | jq '.securityGroupID' | tr -d '"')
    vars[PUBLIC_ZONE_ID]=$(cat ${infraOutfile} | jq '.publicZoneID' | tr -d '"')
    vars[PRIVATE_ZONE_ID]=$(cat ${infraOutfile} | jq '.privateZoneID' | tr -d '"')
    vars[LOCAL_ZONE_ID]=$(cat ${infraOutfile} | jq '.localZoneID' | tr -d '"')

    echo "$(date) ==== Creating AWS IAM ===="
    echo "$(date) ${HYPERSHIFT_COMMAND} create iam aws --aws-creds ${AWS_CREDS_FILE} --infra-id ${vars[INFRA_ID]} --local-zone-id ${vars[LOCAL_ZONE_ID]} --private-zone-id ${vars[PRIVATE_ZONE_ID]} --public-zone-id ${vars[PUBLIC_ZONE_ID]} --output-file ${iamOutfile}"

    # Create AWS IAM
    ${HYPERSHIFT_COMMAND} create iam aws --aws-creds ${AWS_CREDS_FILE} --infra-id ${vars[INFRA_ID]} --local-zone-id ${vars[LOCAL_ZONE_ID]} --private-zone-id ${vars[PRIVATE_ZONE_ID]} --public-zone-id ${vars[PUBLIC_ZONE_ID]} --output-file ${iamOutfile}
    if [ $? -ne 0 ]; then
        echo "$(date) Failed to create IAM"
        echo "$(date) Destroying the AWS infrastructure"
        exit 1
    fi

    # Set iam resource variables
    vars[PROFILE_NAME]=$(cat ${iamOutfile} | jq '.profileName' | tr -d '"')
    vars[ISSUER_URL]=$(cat ${iamOutfile} | jq '.issuerURL' | tr -d '"')
    vars[ROLES_INGRESS_ARN]=$(cat ${iamOutfile} | jq '.roles .ingressARN' | tr -d '"')
    vars[ROLES_IMG_REGISTRY_ARN]=$(cat ${iamOutfile} | jq '.roles .imageRegistryARN' | tr -d '"')
    vars[ROLES_STORAGE_ARN]=$(cat ${iamOutfile} | jq '.roles .storageARN' | tr -d '"')
    vars[ROLES_NETWORK_ARN]=$(cat ${iamOutfile} | jq '.roles .networkARN' | tr -d '"')
    vars[ROLES_KUBE_CLOUD_CONTROLLER_ARN]=$(cat ${iamOutfile} | jq '.roles .kubeCloudControllerARN' | tr -d '"')
    vars[ROLES_NODEPOOL_MGMT_ARN]=$(cat ${iamOutfile} | jq '.roles .nodePoolManagementARN' | tr -d '"')
    vars[ROLES_CPO_ARN]=$(cat ${iamOutfile} | jq '.roles .controlPlaneOperatorARN' | tr -d '"')

    # Copy the template hostedcluster nodepool manifestwork YAML
    cp ./resources/hosted_cluster_manifestwork.yaml ./${vars[CLUSTER_NAME]}.yaml
    if [ $? -ne 0 ]; then
        echo "$(date) failed to copy hosted_cluster_manifestwork.yaml"
        echo "$(date) Destroying the AWS infrastructure and IAM"
        exit 1
    fi

    # Copy the template htpasswd manifestwork YAML
    cp ./resources/htpasswd.yaml ./${vars[CLUSTER_NAME]}-htpasswd.yaml
    if [ $? -ne 0 ]; then
        echo "$(date) failed to copy htpasswd.yaml"
        echo "$(date) Destroying the AWS infrastructure and IAM"
        exit 1
    fi

    # Copy the template managedcluster YAML
    cp ./resources/managedcluster.yaml ./${vars[CLUSTER_NAME]}-managedcluster.yaml
    if [ $? -ne 0 ]; then
        echo "$(date) failed to copy managedcluster.yaml"
        echo "$(date) Destroying the AWS infrastructure and IAM"
        exit 1
    fi

    # Replace variables with the actual infra and iam values in the manifestworks and managedcluster
    for key in ${!vars[@]}
        do
            value=${vars[${key}]}
            sed -i -e "s|__${key}__|${value}|" ${vars[CLUSTER_NAME]}.yaml
            if [ $? -ne 0 ]; then
                echo "$(date) failed to substitue __${key}__ in ${vars[CLUSTER_NAME]}.yaml"
                echo "$(date) Destroying the AWS infrastructure and IAM"
                exit 1
            fi

            sed -i -e "s|__${key}__|${value}|" ${vars[CLUSTER_NAME]}-htpasswd.yaml
            if [ $? -ne 0 ]; then
                echo "$(date) failed to substitue __${key}__ in ${vars[CLUSTER_NAME]}-htpasswd.yaml"
                echo "$(date) Destroying the AWS infrastructure and IAM"
                exit 1
            fi

            sed -i -e "s|__${key}__|${value}|" ${vars[CLUSTER_NAME]}-managedcluster.yaml
            if [ $? -ne 0 ]; then
                echo "$(date) failed to substitue __${key}__ in ${vars[CLUSTER_NAME]}-managedcluster.yaml"
                echo "$(date) Destroying the AWS infrastructure and IAM"
                exit 1
            fi
        done

    # Apply the managedcluster and manifestworks to get the hosted cluster created in the remote hosting cluster
    ${KUBECTL_COMMAND} apply -f ${vars[CLUSTER_NAME]}-managedcluster.yaml
    if [ $? -ne 0 ]; then
        echo "$(date) failed to apply ${vars[CLUSTER_NAME]}-managedcluster.yaml"
        echo "$(date) Destroying the AWS infrastructure and IAM"
        exit 1
    fi

    ${KUBECTL_COMMAND} apply -f ${vars[CLUSTER_NAME]}.yaml
    if [ $? -ne 0 ]; then
        echo "$(date) failed to apply ${vars[CLUSTER_NAME]}.yaml"
        echo "$(date) Destroying the AWS infrastructure and IAM"
        exit 1
    fi

    ${KUBECTL_COMMAND} apply -f ${vars[CLUSTER_NAME]}-htpasswd.yaml
    if [ $? -ne 0 ]; then
        echo "$(date) failed to apply ${vars[CLUSTER_NAME]}-htpasswd.yaml"
        echo "$(date) Destroying the AWS infrastructure and IAM"
        exit 1
    fi
}

deleteHostedCluster() {
    clusterName=$1
    infraID=$2

    ${KUBECTL_COMMAND} delete -f ${clusterName}-managedcluster.yaml
    if [ $? -ne 0 ]; then
        echo "$(date) failed to delete -f ${clusterName}-managedcluster.yaml"
        echo "$(date) Destroying the AWS infrastructure and IAM"
        exit 1
    fi

    # Verify that the managed cluster is deleted
    waitForManagedClusterDelete ${infraID}

    # Delete the manifestworks
    ${KUBECTL_COMMAND} delete -f ${clusterName}-htpasswd.yaml
    if [ $? -ne 0 ]; then
        echo "$(date) failed to delete -f ${clusterName}-htpasswd.yaml"
        echo "$(date) Destroying the AWS infrastructure and IAM"
        exit 1
    fi

    ${KUBECTL_COMMAND} delete -f ${clusterName}.yaml
    if [ $? -ne 0 ]; then
        echo "$(date) failed to delete -f ${clusterName}.yaml"
        echo "$(date) Destroying the AWS infrastructure and IAM"
        exit 1
    fi

    # Verify that the manifestwork with hostedcluster and nodepool payload is deleted
    waitForManifestworkDelete ${HOSTING_CLUSTER_NAME} ${infraID}
}

cleanup() {
    clusterName=$1
    infraID=$2
    infraFile=$3
    iamFile=$4

    # Remove the files
    rm ${infraFile}
    rm ${iamFile}
    rm ${clusterName}.yaml
    rm ${clusterName}-htpasswd.yaml
    rm ${clusterName}-managedcluster.yaml
    rm ${clusterName}.yaml-e
    rm ${clusterName}-htpasswd.yaml-e
    rm ${clusterName}-managedcluster.yaml-e
}

verifyHostingCluster() {
    hubKubeConfigFile=$1
    hostingClusterName=$2
    hostingKubeConfigFile=$3

    verifyManifestWorkAPI ${hubKubeConfigFile} ${hostingClusterName} ${hostingKubeConfigFile} "/results"
}

verifyManifestWorkAPI() {
    hubKubeConfigFile=$1
    clusterName=$2
    managedKubeConfigFile=$3
    outputDir=$4

    /work-e2e --ginkgo.v --ginkgo.label-filter=sanity-check \
        --ginkgo.junit-report="${outputDir}/foundation-work-e2e-${clusterName}.xml" \
        -hub-kubeconfig=${hubKubeConfigFile} -webhook-deployment-name=cluster-manager-work-webhook \
        -cluster-name=${clusterName} -managed-kubeconfig=${managedKubeConfigFile} -eventually-timeout=180s
    if [ $? -ne 0 ]; then
        echo "$(date) failed to verify the ManifestWork API on cluster ${clusterName}"
        ${KUBECTL_COMMAND} get managedcluster ${clusterName} -o yaml
        processManifestWorkTestReport "${outputDir}/foundation-work-e2e-${clusterName}.xml"
        exit 1
    fi

    processManifestWorkTestReport "${outputDir}/foundation-work-e2e-${clusterName}.xml"
}

processManifestWorkTestReport() {
    testReport=$1

    if [ -e ${testReport} ]
    then
        #set the priority and severity of the test cases
        sed -i -e 's/\[It\]/Server Foundation: \[P2\]\[Sev2\]\[server-foundation\]/' \
            -e 's/"\[BeforeSuite\]"/"Server Foundation: \[P2\]\[Sev2\]\[server-foundation\] BeforeSuite"/' \
            -e 's/"\[AfterSuite\]"/"Server Foundation: \[P2\]\[Sev2\]\[server-foundation\] AfterSuite"/' \
            ${testReport}
        if [ $? -ne 0 ]; then
            echo "$(date) failed to set the priority and severity of the test cases in ${testReport}"
            exit 1
        fi
    fi
}

verifyHostedCluster() {
    FOUND=1
    SECONDS=0
    infraId=$1

    managedClusterImported=false  
    hostedClusterCompleted=false
    nodePoolReady=false

    while [ ${FOUND} -eq 1 ]; do
        # Wait up to 45 minutes, re-try every 30 seconds
        if [ $SECONDS -gt 2700 ]; then
            echo "$(date) Timeout waiting for a successful provisioning of hosted cluster."
            ${KUBECTL_COMMAND} get managedcluster ${infraId} -o yaml
            echo "$(date) Destroying the AWS infrastructure and IAM"
            exit 1
        fi

        # Wait for the managed cluster to become joined and available
        HubAcceptedManagedCluster=`${KUBECTL_COMMAND} get managedcluster ${infraId} -o jsonpath='{.status.conditions[?(@.type=="HubAcceptedManagedCluster")].status}'`
        ManagedClusterJoined=`${KUBECTL_COMMAND} get managedcluster ${infraId} -o jsonpath='{.status.conditions[?(@.type=="ManagedClusterJoined")].status}'`
        ManagedClusterConditionAvailable=`${KUBECTL_COMMAND} get managedcluster ${infraId} -o jsonpath='{.status.conditions[?(@.type=="ManagedClusterConditionAvailable")].status}'`
        ManagedClusterURL=`${KUBECTL_COMMAND} get managedcluster ${infraId} -o jsonpath='{.spec.managedClusterClientConfigs[0].url}'`

        if [[ ("$HubAcceptedManagedCluster" == "True") && ("$ManagedClusterJoined" == "True") && ("$ManagedClusterConditionAvailable" == "True") && ("$ManagedClusterURL" > "") ]]; then
            echo "$(date) Managed cluster: imported"
            managedClusterImported=true
        else
            echo "$(date) Managed cluster: pending import"
        fi

        # Check the manifestwork status feedback to verify that the hosted cluster is avaiable
        HostedClusterStatusFeedback=`${KUBECTL_COMMAND} get manifestwork ${infraId} -n ${HOSTING_CLUSTER_NAME} -o jsonpath='{.status.resourceStatus}' | jq '.manifests[] | select(.resourceMeta.kind=="HostedCluster").statusFeedback.values[]'`
        overallProgressStatus=`echo ${HostedClusterStatusFeedback} | jq 'select(.name=="progress").fieldValue.string'`
        hcpAvailableStatus=`echo ${HostedClusterStatusFeedback} | jq 'select(.name=="Available-Status").fieldValue.string'`
        progressingStatus=`echo ${HostedClusterStatusFeedback} | jq 'select(.name=="Progressing-Status").fieldValue.string'`
        degradedStatus=`echo ${HostedClusterStatusFeedback} | jq 'select(.name=="Degraded-Status").fieldValue.string'`
        ignitionEndpointStatus=`echo ${HostedClusterStatusFeedback} | jq 'select(.name=="IgnitionEndpointAvailable-Status").fieldValue.string'`
        infraReadyStatus=`echo ${HostedClusterStatusFeedback} | jq 'select(.name=="InfrastructureReady-Status").fieldValue.string'`
        kubeAPIServerReadyStatus=`echo ${HostedClusterStatusFeedback} | jq 'select(.name=="KubeAPIServerAvailable-Status").fieldValue.string'`

        if [[ ("$overallProgressStatus" == "\"Completed\"") && \
                ("$hcpAvailableStatus" == "\"True\"") && \
                ("$progressingStatus" == "\"False\"") && \
                ("$degradedStatus" == "\"False\"") && \
                ("$ignitionEndpointStatus" == "\"True\"") && \
                ("$infraReadyStatus" == "\"True\"") && \
                ("$kubeAPIServerReadyStatus" == "\"True\"") ]]; then
            echo "$(date) HostedCluster: ${overallProgressStatus}"
            hostedClusterCompleted=true
        else
            echo "$(date) HostedCluster: ${overallProgressStatus}"
        fi

        # Check the manifestwork status feedback to verify that the node pool is avaiable
        NpdePoolStatusFeedback=`${KUBECTL_COMMAND} get manifestwork ${infraId} -n ${HOSTING_CLUSTER_NAME} -o jsonpath='{.status.resourceStatus}' | jq '.manifests[] | select(.resourceMeta.kind=="NodePool").statusFeedback.values[]'`
        readyStatus=`echo ${NpdePoolStatusFeedback} | jq 'select(.name=="Ready-Status").fieldValue.string'`

        if [[ ("$readyStatus" == "\"True\"") ]]; then
            echo "$(date) NodePool: ready"
            nodePoolReady=true
        else
            echo "$(date) NodePool: not ready"
        fi

        if [[ ("$managedClusterImported" == true) && ("$hostedClusterCompleted" == true) && ("$nodePoolReady" == true) ]]; then
            break
        fi

        sleep 30
        (( SECONDS = SECONDS + 30 ))
    done

    if [ "${ENABLE_FOUNDATION_CANARY}" == "true" ]; then
        # Verify ManifestWork API on the hosted cluster
        echo "$(date) ==== Verifying ManifestWork API on hosted cluster  ${infraId} ===="
        ${KUBECTL_COMMAND} -n ${HOSTING_CLUSTER_NAME} get secret "${infraId}-admin-kubeconfig" -o jsonpath={.data\.kubeconfig} | base64 -d > "kubeconfig.${infraId}"
        verifyManifestWorkAPI ${HUB_KUBECONFIG} ${infraId} "kubeconfig.${infraId}" "/results"
    fi
}


waitForManagedClusterDelete() {
    FOUND=1
    SECONDS=0

    resName=$1

    while [ ${FOUND} -eq 1 ]; do
        # Wait up to 30 minutes
        if [ $SECONDS -gt 1800 ]; then
            echo "$(date) Timed out waiting for managed cluster ${resName} to be deleted."
            ${KUBECTL_COMMAND} get managedcluster ${resName} -o yaml
            exit 1
        fi

        ${KUBECTL_COMMAND} get managedcluster ${resName}
        if [ $? -eq 0 ]; then
            echo "$(date) managed cluster ${resName} still exists"
        else
            echo "$(date) managed cluster ${resName} not found"
            break
        fi

        sleep 30
        (( SECONDS = SECONDS + 30 ))
    done
}

waitForManifestworkDelete() {
    FOUND=1
    SECONDS=0

    resNamespace=$1
    resName=$2

    while [ ${FOUND} -eq 1 ]; do
        # Wait up to 30 minutes
        if [ $SECONDS -gt 1800 ]; then
            echo "$(date) Timed out waiting for manifestwork ${resNamespace}/${resName} to be deleted."
            ${KUBECTL_COMMAND} get manifestwork ${resName} -n ${resNamespace} -o yaml
            exit 1
        fi

        ${KUBECTL_COMMAND} get manifestwork ${resName} -n ${resNamespace}
        if [ $? -eq 0 ]; then
            echo "$(date) manifestwork ${resNamespace}/${resName} still exists"
        else
            echo "$(date) manifestwork ${resNamespace}/${resName} not found"
            break
        fi

        sleep 30
        (( SECONDS = SECONDS + 30 ))
    done
}

enableHypershiftForLocalCluster() {
    ${KUBECTL_COMMAND} get secret hypershift-operator-oidc-provider-s3-credentials -n local-cluster
    if [ $? -ne 0 ]; then
        # Create secrets for hypershift operator installation
        ${KUBECTL_COMMAND} create secret generic hypershift-operator-oidc-provider-s3-credentials --from-file=credentials=${AWS_CREDS_FILE} --from-literal=bucket=${S3_BUCKET_NAME} --from-literal=region=${REGION} -n local-cluster
        if [ $? -ne 0 ]; then
            echo "$(date) failed to create secret hypershift-operator-oidc-provider-s3-credentials"
            exit 1
        fi
    fi

    ${KUBECTL_COMMAND} get secret hypershift-operator-external-dns-credentials -n local-cluster
    if [ $? -ne 0 ]; then
        ${KUBECTL_COMMAND} create secret generic hypershift-operator-external-dns-credentials --from-file=credentials=${AWS_CREDS_FILE} --from-literal=provider=aws --from-literal=domain-filter=${EXT_DNS_DOMAIN} -n local-cluster
        if [ $? -ne 0 ]; then
            echo "$(date) failed to acreate secret hypershift-operator-external-dns-credentials"
            exit 1
        fi
    fi

    # Enable the hypershift feature. This also installs the hypershift addon for local-cluster
    ${KUBECTL_COMMAND} patch mce multiclusterengine --type=merge -p '{"spec":{"overrides":{"components":[{"name":"hypershift","enabled": true}]}}}'
    if [ $? -ne 0 ]; then
        echo "$(date) failed to enable hypershift in MCE"
        exit 1
    fi

    # Wait for hypershift-addon to be available
    FOUND=1
    SECONDS=0
    running="\([0-9]\+\)\/\1"
    while [ ${FOUND} -eq 1 ]; do
        # Wait up to 5min
        if [ $SECONDS -gt 300 ]; then
            echo "Timeout waiting for hypershift-addon to be available."
            echo "List of current pods:"
            ${KUBECTL_COMMAND} get managedclusteraddon hypershift-addon -n local-cluster -o yaml
            exit 1
        fi

        addonAvailable=`${KUBECTL_COMMAND} get managedclusteraddon hypershift-addon -n local-cluster -o jsonpath='{.status.conditions[?(@.type=="Available")].status}'`
        addonDegraded=`${KUBECTL_COMMAND} get managedclusteraddon hypershift-addon -n local-cluster -o jsonpath='{.status.conditions[?(@.type=="Degraded")].status}'`

        if [[ ("$addonAvailable" == "True") && ("$addonDegraded" == "False") ]]; then 
            echo "Hypershift addon is available"
            break
        fi
        sleep 10
        (( SECONDS = SECONDS + 10 ))
    done
}

installOcBinary() {
  DOWNLOAD_URL=$(${KUBECTL_COMMAND} get route downloads -n openshift-console --output jsonpath={.spec.host})
  if [[ -n $DOWNLOAD_URL ]];
  then
    echo "$(date) Found the download URL: \"$DOWNLOAD_URL\""
  else
    echo "$(date) No download URL found."
    exit 1
  fi

  curl -k -LO https://${DOWNLOAD_URL}/amd64/linux/oc.tar
  if [ $? -ne 0 ];
  then
    echo "$(date) oc.tar download failed"
    exit 1
  fi

  tar -xvf oc.tar
  if [ $? -ne 0 ];
  then
    echo "$(date) failed to decompress oc.tar"
    exit 1
  fi

  chmod +x oc
  if [ $? -ne 0 ];
  then
    echo "$(date) failed to chmod +x oc"
    exit 1
  fi

  mv oc /bin
  if [ $? -ne 0 ];
  then
    echo "$(date) failed to move oc to /bin"
    exit 1
  fi
}

installHypershiftBinary() {
  # hcp CLI from the hcp-cli-download ConsoleCLIDownload, which is the productized version of the CLI does not support
  # creating "infra" and "iam". So the developer version of the CLI needs to be extracted from the hypershift operator pod
  ${KUBECTL_COMMAND} get namespace hypershift

  if [ $? -ne 0 ];
  then
    echo "$(date) hypershift namespace not found"
    exit 1
  fi

  # Get a running hypershift operator pod
  ${OC_COMMAND} project hypershift
  HO_POD_NAME=$(${KUBECTL_COMMAND} get pod --no-headers=true --field-selector=status.phase=Running -l app=operator -o custom-columns="NAME:.metadata.name" | head -n 1)

  if [[ -n $HO_POD_NAME ]];
  then
    echo "$(date) Found a running hypershift operator pod: \"$HO_POD_NAME\""
  else
    echo "$(date) No running hypershift operator pod found."
    exit 1
  fi

  # Extract the hypershift CLI from the hypershift operator pod. hypershift-no-cgo is built with no CGO enabled. 
  ${OC_COMMAND} rsync ${HO_POD_NAME}:/usr/bin/hypershift-no-cgo /tmp
  if [ $? -ne 0 ]; then
      echo "$(date) failed to extract hypershift CLI from the hypershift operator pod"
      exit 1
  fi

  mv /tmp/hypershift-no-cgo /tmp/hypershift
  if [ $? -ne 0 ]; then
    echo "$(date) failed to mv /tmp/hypershift-no-cgo /tmp/hypershift"
    exit 1
  fi

  chmod +x /tmp/hypershift
  if [ $? -ne 0 ]; then
    echo "$(date) failed to chmod +x /tmp/hypershift"
    exit 1
  fi

  mv /tmp/hypershift /bin
  if [ $? -ne 0 ]; then
    echo "$(date) failed to mv extracted hypershift binary to /bin"
    exit 1
  fi
}

enableHostedModeAddon() {
    ${KUBECTL_COMMAND} apply -f resources/addonconfig.yaml
    if [ $? -ne 0 ]; then
        echo "$(date) failed to apply resources/addonconfig.yaml"
        exit 1
    fi

    ${KUBECTL_COMMAND} patch clustermanagementaddon work-manager --type merge -p '{"spec":{"supportedConfigs":[{"defaultConfig":{"name":"addon-hosted-config","namespace":"multicluster-engine"},"group":"addon.open-cluster-management.io","resource":"addondeploymentconfigs"}]}}'
    if [ $? -ne 0 ]; then
        echo "$(date) failed to patch clustermanagementaddon work-manager"
        exit 1
    fi

    ${KUBECTL_COMMAND} patch clustermanagementaddon config-policy-controller --type merge -p '{"spec":{"supportedConfigs":[{"defaultConfig":{"name":"addon-hosted-config","namespace":"multicluster-engine"},"group":"addon.open-cluster-management.io","resource":"addondeploymentconfigs"}]}}'

    ${KUBECTL_COMMAND} patch clustermanagementaddon cert-policy-controller --type merge -p '{"spec":{"supportedConfigs":[{"defaultConfig":{"name":"addon-hosted-config","namespace":"multicluster-engine"},"group":"addon.open-cluster-management.io","resource":"addondeploymentconfigs"}]}}'
}

echo "$(date) ==== Enable hypershift feature ===="
enableHypershiftForLocalCluster

if ! command -v ${HYPERSHIFT_COMMAND} &> /dev/null
then
    echo "$(date) ==== Installing hypershift binary ===="
    installOcBinary
    installHypershiftBinary
fi

# Enabled hosted mode addons
# https://github.com/stolostron/hypershift-addon-operator/blob/main/docs/running_mce_acm_addons_hostedmode.md
echo "$(date) ==== Enable hosted mode addon configuration ===="
enableHostedModeAddon

if [ "${ENABLE_FOUNDATION_CANARY}" == "true" ]; then
    # Only verify the hosting cluster when the hosting cluster is local-cluster.
    # If other managed cluster is chosen, there is no way to get the kubeconfig of the hosting cluster,
    # which is required to run the foundation canary test.
    # TODO: support hosting cluster other than local-cluster
    if [ "${HOSTING_CLUSTER_NAME}" == "local-cluster" ]; then
        # Verify hosting cluster
        echo "$(date) ==== Verifying hosting cluster  ${HOSTING_CLUSTER_NAME} ===="
        verifyHostingCluster ${HUB_KUBECONFIG} ${HOSTING_CLUSTER_NAME} ${HUB_KUBECONFIG}
    fi
fi

# Generate AWS infrastructure and IAM for the first hosted cluster
# Generate the follwing YAMLs:
#   - manifestwork YAML containing HostedCluster and NodePool
#   - manifestwork YAML containing htpasswd for the hosted cluster (OCP user identify provider)
#   - managed cluster YAML to import the hosted cluster
# Then apply them to create a hosted cluster
echo "$(date) ==== Creating hosted cluster  ${CLUSTER_NAME_1} ===="
createHostedCluster ${CLUSTER_NAME_1} ${INFRA_ID_1} ${CLUSTER_UUID_1} ${INFRA_OUTPUT_FILE_1} ${IAM_OUTPUT_FILE_1}

# Generate AWS infrastructure and IAM for the second hosted cluster
# The output of this is:
#   - manifestwork YAML containing HostedCluster and NodePool
#   - manifestwork YAML containing htpasswd for the hosted cluster (OCP user identify provider)
#   - managed cluster YAML to import the hosted cluster
# Then apply them to create a hosted cluster
echo "$(date) ==== Creating hosted cluster  ${CLUSTER_NAME_2} ===="
createHostedCluster ${CLUSTER_NAME_2} ${INFRA_ID_2} ${CLUSTER_UUID_2} ${INFRA_OUTPUT_FILE_2} ${IAM_OUTPUT_FILE_2}

sleep 30

# Verify that the managed cluster is imported, hosted cluster and node pool are available
# This also verifies that we can log into the hosted cluster's API server using the user defined in htpasswd
echo "$(date) ==== Verifying hosted cluster  ${CLUSTER_NAME_1} ===="
verifyHostedCluster ${INFRA_ID_1}

echo "$(date) ==== Verifying hosted cluster  ${CLUSTER_NAME_2} ===="
verifyHostedCluster ${INFRA_ID_2}

# Test ran successfully, remove the failed result file and put the successful file in
rm -f /results/hypershift-failed.xml
cp /hypershift-success.xml /results

# Delete the first managed cluster
echo "$(date) ==== Deleting hosted cluster  ${CLUSTER_NAME_1} ===="
deleteHostedCluster ${CLUSTER_NAME_1} ${INFRA_ID_1}

# Delete the second managed cluster
echo "$(date) ==== Deleting hosted cluster  ${CLUSTER_NAME_2} ===="
deleteHostedCluster ${CLUSTER_NAME_2} ${INFRA_ID_2}

# Destroy infra, IAM and remove files
echo "$(date) ==== Cleaning up hosted cluster  ${CLUSTER_NAME_1} ===="
cleanup ${CLUSTER_NAME_1} ${INFRA_ID_1} ${INFRA_OUTPUT_FILE_1} ${IAM_OUTPUT_FILE_1}

echo "$(date) ==== Cleaning up hosted cluster  ${CLUSTER_NAME_2} ===="
cleanup ${CLUSTER_NAME_2} ${INFRA_ID_2} ${INFRA_OUTPUT_FILE_2} ${IAM_OUTPUT_FILE_2}

exit 0
