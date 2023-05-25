# Hypershift Hosted Cluster Creation Failures in MCE 2.2.z Cluster

After the security policy change in S3 https://aws.amazon.com/blogs/aws/heads-up-amazon-s3-security-changes-are-coming-in-april-of-2023 in April 2023. If you creates a S3 bucket for the HyperShift operator after this security policy change and you have MCE 2.2.x, the hypershift hosted cluster provisioning on AWS platform will most likely fail with the following error in the hypershift operator.

```
{"level":"error","ts":"2023-05-18T13:05:41Z","msg":"Reconciler error","controller":"hostedcluster","controllerGroup":"hypershift.openshift.io","controllerKind":"HostedCluster","hostedCluster":{"name":"brcox-hypershift-arm","namespace":"clusters"},"namespace":"clusters","name":"brcox-hypershift-arm","reconcileID":"d62f234d-bd22-409f-aaa8-32016c9ac024","error":"failed to reconcile the AWS OIDC documents: failed to upload /.well-known/openid-configuration to the brcox-bucket-new s3 bucket: aws returned an error: AccessControlListNotSupported","errorCauses":[{"error":"failed to reconcile the AWS OIDC documents: failed to upload /.well-known/openid-configuration to the brcox-bucket-new s3 bucket: aws returned an error: 
```

This problem will be fixed in the upcoming MCE 2.2.5 release. In order to work around the problem, you need to configure your S3 bucket so that the hypershift operator can create OIDC documents in the bucket. Run the following command.

```
$ export BUCKET_NAME=your_bucket_name
$ aws s3api put-bucket-ownership-controls --bucket $BUCKET_NAME --ownership-controls="Rules=[{ObjectOwnership=BucketOwnerPreferred}]"
```

