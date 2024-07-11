# Creating a role and STS credentials for AWS

If you plan to create and manage hosted clusters on AWS, complete the following steps to create an AWS IAM role for hcp CLI to assume. The role ARN you create here is used with `--role-arn` flag in hcp CLI commands for AWS.

## Create an IAM policy for the CLI role.

To create the IAM policy for the hcp CLI role, enter the following command. The role ARN you create here does not expire and can be re-used to run `hcp` commands that requires the role ARN.

```
echo '{
    "Version": "2012-10-17",
    "Statement": [
        {
            "Sid": "EC2",
            "Effect": "Allow",
            "Action": [
                "ec2:CreateDhcpOptions",
                "ec2:DeleteSubnet",
                "ec2:ReplaceRouteTableAssociation",
                "ec2:DescribeAddresses",
                "ec2:DescribeInstances",
                "ec2:DeleteVpcEndpoints",
                "ec2:CreateNatGateway",
                "ec2:CreateVpc",
                "ec2:DescribeDhcpOptions",
                "ec2:AttachInternetGateway",
                "ec2:DeleteVpcEndpointServiceConfigurations",
                "ec2:DeleteRouteTable",
                "ec2:AssociateRouteTable",
                "ec2:DescribeInternetGateways",
                "ec2:DescribeAvailabilityZones",
                "ec2:CreateRoute",
                "ec2:CreateInternetGateway",
                "ec2:RevokeSecurityGroupEgress",
                "ec2:ModifyVpcAttribute",
                "ec2:DeleteInternetGateway",
                "ec2:DescribeVpcEndpointConnections",
                "ec2:RejectVpcEndpointConnections",
                "ec2:DescribeRouteTables",
                "ec2:ReleaseAddress",
                "ec2:AssociateDhcpOptions",
                "ec2:TerminateInstances",
                "ec2:CreateTags",
                "ec2:DeleteRoute",
                "ec2:CreateRouteTable",
                "ec2:DetachInternetGateway",
                "ec2:DescribeVpcEndpointServiceConfigurations",
                "ec2:DescribeNatGateways",
                "ec2:DisassociateRouteTable",
                "ec2:AllocateAddress",
                "ec2:DescribeSecurityGroups",
                "ec2:RevokeSecurityGroupIngress",
                "ec2:CreateVpcEndpoint",
                "ec2:DescribeVpcs",
                "ec2:DeleteSecurityGroup",
                "ec2:DeleteDhcpOptions",
                "ec2:DeleteNatGateway",
                "ec2:DescribeVpcEndpoints",
                "ec2:DeleteVpc",
                "ec2:CreateSubnet",
                "ec2:DescribeSubnets"
            ],
            "Resource": "*"
        },
        {
            "Sid": "ELB",
            "Effect": "Allow",
            "Action": [
                "elasticloadbalancing:DeleteLoadBalancer",
                "elasticloadbalancing:DescribeLoadBalancers",
                "elasticloadbalancing:DescribeTargetGroups",
                "elasticloadbalancing:DeleteTargetGroup"
            ],
            "Resource": "*"
        },
        {
            "Sid": "IAMPassRole",
            "Effect": "Allow",
            "Action": "iam:PassRole",
            "Resource": "arn:*:iam::*:role/*-worker-role",
            "Condition": {
                "ForAnyValue:StringEqualsIfExists": {
                    "iam:PassedToService": "ec2.amazonaws.com"
                }
            }
        },
        {
            "Sid": "IAM",
            "Effect": "Allow",
            "Action": [
                "iam:CreateInstanceProfile",
                "iam:DeleteInstanceProfile",
                "iam:GetRole",
                "iam:UpdateAssumeRolePolicy",
                "iam:GetInstanceProfile",
                "iam:TagRole",
                "iam:RemoveRoleFromInstanceProfile",
                "iam:CreateRole",
                "iam:DeleteRole",
                "iam:PutRolePolicy",
                "iam:AddRoleToInstanceProfile",
                "iam:CreateOpenIDConnectProvider",
                "iam:ListOpenIDConnectProviders",
                "iam:DeleteRolePolicy",
                "iam:UpdateRole",
                "iam:DeleteOpenIDConnectProvider",
                "iam:GetRolePolicy"
            ],
            "Resource": "*"
        },
        {
            "Sid": "Route53",
            "Effect": "Allow",
            "Action": [
                "route53:ListHostedZonesByVPC",
                "route53:CreateHostedZone",
                "route53:ListHostedZones",
                "route53:ChangeResourceRecordSets",
                "route53:ListResourceRecordSets",
                "route53:DeleteHostedZone",
                "route53:AssociateVPCWithHostedZone",
                "route53:ListHostedZonesByName"
            ],
            "Resource": "*"
        },
        {
            "Sid": "S3",
            "Effect": "Allow",
            "Action": [
                "s3:ListAllMyBuckets",
                "s3:ListBucket",
                "s3:DeleteObject",
                "s3:DeleteBucket"
            ],
            "Resource": "*"
        }
    ]
}' > cli-policy.json
```

Run this AWS command to create the policy.

```
aws iam create-policy --policy-name hcp-cli-policy --policy-document file://cli-policy.json
```

To get the current IAM identity ARN, enter the following code. The ARN from this command output will be used in the next step.

```
aws sts get-caller-identity
```

Enter the following code to create the IAM role for the hcp CLI. Replace <IAM_identity_ANR> with the actual ARN from the previous command.

```
echo '{
    "Version": "2012-10-17",
    "Statement": [
        {
            "Effect": "Allow",
            "Principal": {
                "AWS": "<IAM_identity_ANR>"
            },
            "Action": "sts:AssumeRole"
        }
    ]
}' > trust-policy.json
```

Run the following command to create the role.

```
aws iam create-role --role-name hcp-cli-role --assume-role-policy-document file://trust-policy.json
```

Enter the following code to attach the IAM policy to the hcp CLI role. Replace <hcp-cli-policy-ARN> with the actual policy ARN from the previous step.

```
aws iam attach-role-policy --policy-arn <hcp-cli-policy-ARN> --role-name hcp-cli-role
```

## Creating the AWS Security Token Service (STS) credentials

If you plan to create and manage hosted clusters on AWS, complete the following steps to create an AWS STS credential files that is used with -`-sts-creds` flag in hcp CLI commands for AWS. The session token expires in 12 hours by default. Use `--duration-seconds` option to customize the expiry. You might have to regenerate this STS credentials to run `hcp` command in the future.

```
aws sts get-session-token --output json > sts-creds.json
```