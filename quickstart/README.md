# Instructions
This document describes how to quickly get started with Hosting Control Planes and ACM/MCE

## Requirements
1. OpenShift Cluster, version 4.10+ is recommended
2. MCE or ACM installed on this cluster from Operator Hub. (Alternate: https://github.com/stolostron/deploy)
3. AWS artifacts:
   * `AWS Service Account` Key & Secret Key with S3 permissions (ONLY needs S3 bucket permissions)
      ```shell
      # ./s3.creds
      [default]
      aws_access_key_id = MY_ACCESS_KEY_ID
      aws_secret_access_key = MY SECRET_ACCESS_KEY
      ```
   * S3 Bucket name (user comes with an existing bucket or can use `create-s3-bucket.sh` script)
      
        Bucket settings:
      * `ACLs enabled`, Object Ownership `Object writer`
      * For the Access control list (ACL)
         * Bucket owner:
            ```
            Object: List, Write
            Bucket ACL: Read, Write
            ```
      * Uncheck `Block all public access`
      * Disable `Bucket Versioning`
      * Disable `Default encryption`
      
   * S3 Bucket region (this is related to where the bucket was created)

## Quickstart
* Make sure you are connected to the OpenShift cluster
* Run the `mce-start.sh` or `acm-start.sh command
  * If the environment variables `BUCKET_NAME`, `BUCKET_REGION` and `S3_CREDS` is not set, you are prompted for these values

## What it does
1. Enables preview_hypershift
2. Creates a `local-cluster` `managedCluster` for the OpenShift cluster you are installing to if it doesn't
3. Imports the `local-cluster` if it doesn't exist
4. Applies the Hosting Service Cluster addon (Hypershift) to the `local-cluster` (Hub) if it doesn't exist

## Use the oc hcp or hypershift CLI to create a HostedCluster and NodePool
1. Get the `oc hcp` cli:
https://github.com/stolostron/hypershift-addon-operator/blob/main/docs/installing_hypershift_cli.md
2. `oc hcp --help` to get a list of command parameters

Alternatively, use the Hypershift CLI:
1. Get the `hypershift` Linux cli:
```shell
# Docker command, this places the hypershift binary in your $HOME directory
docker run -it -v $HOME:/tmp --entrypoint /bin/bash quay.io/stolostron/hypershift-operator:4.11 -c 'cp /usr/bin/hypershift /tmp'

# Kubectl command, you must be connected to an OCP with MCE or ACM deployed
oc project hypershift
oc rsync $(oc get pod --output=jsonpath={.items..metadata.name}):/usr/bin/hypershift <your_local-dir>
```
2. `hypershift --help` to get a list of command parameters