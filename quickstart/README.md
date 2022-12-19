# Instructions
This document describes how to quickly get started with Hosting Control Planes and ACM/MCE

## Requirements
1. OpenShift Cluster, version 4.11+ is recommended
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
* Run the `make quickstart` or `quickstart/start.sh` command
  * If the environment variables `BUCKET_NAME`, `BUCKET_REGION` and `S3_CREDS` is not set, you are prompted for these values

## What it does
1. Enables preview_hypershift
2. Create the S3 credential secret for the local-cluster (self hosting)

## Use the HyperShift CLI to create a HostedCluster and NodePool
1. Get the `hypershift` CLI:
https://github.com/stolostron/hypershift-addon-operator/blob/main/docs/installing_hypershift_cli.md

2. Alternate locations:

   Get the `hypershift` Linux CLI
   ```shell
   # Docker command, this places the HyperShift binary in your $HOME directory
   docker run -it -v $HOME:/tmp --entrypoint /bin/bash quay.io/stolostron/hypershift-operator:4.11 -c 'cp /usr/bin/hypershift /tmp'

   # Kubectl command, you must be connected to an OCP with MCE or ACM deployed
   oc project hypershift
   oc rsync $(oc get pod --output=jsonpath={.items..metadata.name}):/usr/bin/hypershift <your_local-dir>
   ```

3. `hypershift --help` to get a list of command parameters

## Create a cluster with the CLI
1. In the MCE/ACM `All Clusters > Credentials` console, create an AWS credential in namespace `default` with name `my-aws`
2. Use the following `hypershift` command to create a cluster:
   ```shell
   hypershift create cluster aws --name my-cluster --namespace default --secret-creds my-aws --region us-east-1 --instance-type t3.xlarge --node-pool-replicas 1
   ```
   This creates a single worker node hosted cluster, using the `my-aws` credential in us-east-1