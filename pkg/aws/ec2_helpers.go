package aws

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/go-logr/logr"
	"github.com/konflux-ci/multi-platform-controller/pkg/cloud"
	v1 "k8s.io/api/core/v1"
	types2 "k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// pingIPAddress tries to connect to the SSH port on ipAddress with a 60-second timeout.
// An error is returned if the connection fails.
func pingIPAddress(ipAddress string) error {
	conn, err := net.DialTimeout("tcp", ipAddress+":22", 60*time.Second)
	if err != nil {
		return err
	}
	return conn.Close()
}

// validateIPAddress returns the IP address of the EC2 instance after verifying SSH connectivity.
// Returns an error if no IP is available or if the instance is not reachable via SSH.
func (ec AWSEc2DynamicConfig) validateIPAddress(ctx context.Context, instance *types.Instance) (string, error) {
	log := logr.FromContextOrDiscard(ctx)
	var ip string

	// Select IP address based on priority: PublicDnsName > PublicIpAddress > PrivateIpAddress
	if instance.PublicDnsName != nil && *instance.PublicDnsName != "" {
		ip = *instance.PublicDnsName
	} else if instance.PublicIpAddress != nil && *instance.PublicIpAddress != "" {
		ip = *instance.PublicIpAddress
	} else if instance.PrivateIpAddress != nil && *instance.PrivateIpAddress != "" && !ec.StrictPublicAddress {
		ip = *instance.PrivateIpAddress
	}

	// Return error if no IP address is available
	if ip == "" {
		return "", fmt.Errorf("instance %s has no accessible IP address", aws.ToString(instance.InstanceId))
	}

	// Verify SSH connectivity
	ping := pingIPAddress
	if ec.pingFunc != nil {
		ping = ec.pingFunc
	}
	if err := ping(ip); err != nil {
		log.Error(err, "failed to connect to AWS instance via SSH",
			"instanceID", aws.ToString(instance.InstanceId),
			"ipAddress", ip)
		return "", fmt.Errorf("failed to resolve IP address %s: %w", ip, err)
	}

	log.Info("Successfully validated IP address",
		"instanceID", aws.ToString(instance.InstanceId),
		"ipAddress", ip)

	return ip, nil
}

// getEC2Client returns the injected mock client if set, otherwise builds a real
// EC2 client from AWS credentials.
func (ec AWSEc2DynamicConfig) getEC2Client(kubeClient client.Client, ctx context.Context) (ec2API, error) {
	if ec.ec2Client != nil {
		return ec.ec2Client, nil
	}
	secretCredentials := SecretCredentialsProvider{Name: ec.Secret, Namespace: ec.SystemNamespace, Client: kubeClient}
	cfg, err := config.LoadDefaultConfig(ctx,
		config.WithCredentialsProvider(secretCredentials),
		config.WithRegion(ec.Region))
	if err != nil {
		return nil, fmt.Errorf("failed to create an AWS config for an EC2 client: %w", err)
	}
	return ec2.NewFromConfig(cfg), nil
}

// configureInstance creates and returns an EC2 instance configuration.
func (ec AWSEc2DynamicConfig) configureInstance(taskRunName string, instanceTag string, additionalInstanceTags map[string]string) (*ec2.RunInstancesInput, error) {
	// Validate that MacOS-specific fields are either all set or all empty
	tenancySet := ec.Tenancy != ""
	hostResourceGroupSet := ec.HostResourceGroupArn != ""
	licenseConfigSet := ec.LicenseConfigurationArn != ""

	if tenancySet || hostResourceGroupSet || licenseConfigSet {
		if !tenancySet || !hostResourceGroupSet || !licenseConfigSet {
			return nil, errors.New("MacOS dedicated host configuration requires all three fields: Tenancy, HostResourceGroupArn, and LicenseConfigurationArn must be set together")
		}
	}

	var subnet *string
	var securityGroups []string
	var securityGroupIds []string
	var instanceProfile *types.IamInstanceProfileSpecification
	var placement *types.Placement
	var licenseSpecifications []types.LicenseConfigurationRequest

	if ec.SubnetId != "" {
		subnet = aws.String(ec.SubnetId)
	}
	if ec.SecurityGroup != "" {
		securityGroups = []string{ec.SecurityGroup}
	}
	if ec.SecurityGroupId != "" {
		securityGroupIds = []string{ec.SecurityGroupId}
	}
	if ec.InstanceProfileName != "" || ec.InstanceProfileArn != "" {
		instanceProfile = &types.IamInstanceProfileSpecification{}
		if ec.InstanceProfileName != "" {
			instanceProfile.Name = aws.String(ec.InstanceProfileName)
		}
		if ec.InstanceProfileArn != "" {
			instanceProfile.Arn = aws.String(ec.InstanceProfileArn)
		}
	}

	// Configure placement and license for dedicated hosts (e.g., Mac instances)
	// All three fields (Tenancy, HostResourceGroupArn, LicenseConfigurationArn) are validated to be set together
	if ec.Tenancy != "" {
		placement = &types.Placement{
			Tenancy:              types.Tenancy(ec.Tenancy),
			HostResourceGroupArn: aws.String(ec.HostResourceGroupArn),
		}
		licenseSpecifications = []types.LicenseConfigurationRequest{
			{LicenseConfigurationArn: aws.String(ec.LicenseConfigurationArn)},
		}
	}

	instanceTags := []types.Tag{
		{Key: aws.String(MultiPlatformManaged), Value: aws.String("true")},
		{Key: aws.String(cloud.InstanceTag), Value: aws.String(instanceTag)},
		{Key: aws.String("Name"), Value: aws.String("multi-platform-builder-" + taskRunName)},
	}
	for k, v := range additionalInstanceTags {
		instanceTags = append(instanceTags, types.Tag{Key: aws.String(k), Value: aws.String(v)})
	}

	return &ec2.RunInstancesInput{
		KeyName:            aws.String(ec.KeyName),
		ImageId:            aws.String(ec.Ami), //ARM RHEL
		InstanceType:       types.InstanceType(ec.InstanceType),
		MinCount:           aws.Int32(1),
		MaxCount:           aws.Int32(1),
		EbsOptimized:       aws.Bool(true),
		SecurityGroups:     securityGroups,
		SecurityGroupIds:   securityGroupIds,
		IamInstanceProfile: instanceProfile,
		SubnetId:           subnet,
		UserData:           ec.UserData,
		BlockDeviceMappings: []types.BlockDeviceMapping{{
			DeviceName:  aws.String("/dev/sda1"),
			VirtualName: aws.String("ephemeral0"),
			Ebs: &types.EbsBlockDevice{
				DeleteOnTermination: aws.Bool(true),
				VolumeSize:          aws.Int32(ec.Disk),
				VolumeType:          types.VolumeTypeGp3,
				Iops:                ec.Iops,
				Throughput:          ec.Throughput,
			},
		}},
		InstanceInitiatedShutdownBehavior: types.ShutdownBehaviorTerminate,
		Placement:                         placement,
		LicenseSpecifications:             licenseSpecifications,
		TagSpecifications: []types.TagSpecification{
			{ResourceType: types.ResourceTypeInstance, Tags: instanceTags},
		},
	}, nil
}

// launchFleet launches a single instance through an EC2 Fleet request
// (CreateFleet, type instant) instead of RunInstances, trying the configured
// instance types in list order. All overrides target the single configured
// subnet; per-pool errors from the fleet response are included in the returned
// error so the reconciler's retry and failure reporting see them.
func (ec AWSEc2DynamicConfig) launchFleet(ec2Client ec2API, ctx context.Context, taskRunName string, instanceTag string, additionalInstanceTags map[string]string) (cloud.InstanceIdentifier, error) {
	log := logr.FromContextOrDiscard(ctx)

	if ec.Tenancy != "" || ec.HostResourceGroupArn != "" || ec.LicenseConfigurationArn != "" {
		return "", errors.New("instance-types is not supported for MacOS dedicated host platforms")
	}
	if ec.SubnetId == "" {
		return "", errors.New("instance-types requires subnet-id to be configured")
	}

	launchTemplateName, err := ec.ensureLaunchTemplate(ec2Client, ctx)
	if err != nil {
		return "", fmt.Errorf("failed to ensure launch template for %s: %w", taskRunName, err)
	}

	fleetOutput, err := ec2Client.CreateFleet(ctx, ec.configureFleet(launchTemplateName, taskRunName, instanceTag, additionalInstanceTags))
	if err != nil {
		return "", fmt.Errorf("failed to launch EC2 fleet for %s: %w", taskRunName, err)
	}
	for _, fleetError := range fleetOutput.Errors {
		log.Info("EC2 fleet capacity pool error",
			"errorCode", aws.ToString(fleetError.ErrorCode),
			"errorMessage", aws.ToString(fleetError.ErrorMessage))
	}
	if len(fleetOutput.Instances) > 0 && len(fleetOutput.Instances[0].InstanceIds) > 0 {
		launched := fleetOutput.Instances[0]
		log.Info("EC2 fleet launched instance",
			"instanceType", launched.InstanceType,
			"instanceId", launched.InstanceIds[0])
		return cloud.InstanceIdentifier(launched.InstanceIds[0]), nil
	}
	// A fleet that could not launch anything returns HTTP 200 with an empty
	// Instances list and one Errors entry per capacity pool.
	return "", fmt.Errorf("failed to launch EC2 fleet for %s: no instances were created: %s",
		taskRunName, fleetErrorsMessage(fleetOutput.Errors))
}

// fleetErrorsMessage renders a fleet response's per-pool errors as
// "code: message" entries, the shape failure events and logs report.
func fleetErrorsMessage(fleetErrors []types.CreateFleetError) string {
	if len(fleetErrors) == 0 {
		return "no error details were returned"
	}
	messages := make([]string, 0, len(fleetErrors))
	for _, fleetError := range fleetErrors {
		messages = append(messages, fmt.Sprintf("%s: %s",
			aws.ToString(fleetError.ErrorCode), aws.ToString(fleetError.ErrorMessage)))
	}
	return strings.Join(messages, "; ")
}

// launchTemplateCache remembers, per launch template name, the hash of the
// platform configuration the controller last applied. It keeps consecutive
// launches from creating a new launch template version every time; entries
// survive provider re-creation on ConfigMap updates because the hash, not the
// object identity, drives version creation.
var launchTemplateCache = struct {
	sync.Mutex
	hashes map[string]string
}{hashes: map[string]string{}}

// launchTemplateName returns the deterministic per-platform launch template name.
func (ec AWSEc2DynamicConfig) launchTemplateName() string {
	return "mpc-" + ec.platformName
}

// launchTemplateHash hashes every configuration field that feeds
// launchTemplateData, so a config change is detected as a hash change.
// Pointers are dereferenced so a recreated, equivalent configuration hashes
// identically instead of triggering a spurious new version.
func (ec AWSEc2DynamicConfig) launchTemplateHash() string {
	key := fmt.Sprintf("%s|%s|%s|%s|%d|%d|%d|%s|%s|%s",
		ec.Ami, ec.KeyName, ec.SecurityGroup, ec.SecurityGroupId, ec.Disk,
		aws.ToInt32(ec.Iops), aws.ToInt32(ec.Throughput), aws.ToString(ec.UserData),
		ec.InstanceProfileName, ec.InstanceProfileArn)
	sum := sha256.Sum256([]byte(key))
	return hex.EncodeToString(sum[:8])
}

// ensureLaunchTemplate makes sure a launch template named mpc-<platform> exists
// and that its default version carries the platform configuration, returning
// the template name. A missing template is created; a template whose
// configuration hash is not in the in-process cache gets a new version that is
// set as the default. Callers reference the template as $Default.
func (ec AWSEc2DynamicConfig) ensureLaunchTemplate(ec2Client ec2API, ctx context.Context) (string, error) {
	if ec.platformName == "" {
		return "", errors.New("platform name is required to manage launch templates")
	}
	name := ec.launchTemplateName()
	hash := ec.launchTemplateHash()

	launchTemplateCache.Lock()
	cachedHash, cached := launchTemplateCache.hashes[name]
	launchTemplateCache.Unlock()

	describeOutput, err := ec2Client.DescribeLaunchTemplates(ctx, &ec2.DescribeLaunchTemplatesInput{
		LaunchTemplateNames: []string{name},
	})
	if err != nil && !strings.Contains(err.Error(), "NotFound") {
		return "", fmt.Errorf("failed to describe launch template %s: %w", name, err)
	}
	exists := err == nil && len(describeOutput.LaunchTemplates) > 0

	if !exists {
		_, err := ec2Client.CreateLaunchTemplate(ctx, &ec2.CreateLaunchTemplateInput{
			LaunchTemplateName: aws.String(name),
			LaunchTemplateData: ec.launchTemplateData(),
			VersionDescription: aws.String("multi-platform-controller initial version"),
		})
		if err != nil {
			return "", fmt.Errorf("failed to create launch template %s: %w", name, err)
		}
	} else if !cached || cachedHash != hash {
		versionOutput, err := ec2Client.CreateLaunchTemplateVersion(ctx, &ec2.CreateLaunchTemplateVersionInput{
			LaunchTemplateName: aws.String(name),
			LaunchTemplateData: ec.launchTemplateData(),
			VersionDescription: aws.String("multi-platform-controller configuration update"),
		})
		if err != nil {
			return "", fmt.Errorf("failed to create launch template version for %s: %w", name, err)
		}
		if versionOutput == nil || versionOutput.LaunchTemplateVersion == nil || versionOutput.LaunchTemplateVersion.VersionNumber == nil {
			return "", fmt.Errorf("creating a launch template version for %s returned no version number", name)
		}
		version := strconv.FormatInt(*versionOutput.LaunchTemplateVersion.VersionNumber, 10)
		_, err = ec2Client.ModifyLaunchTemplate(ctx, &ec2.ModifyLaunchTemplateInput{
			LaunchTemplateName: aws.String(name),
			DefaultVersion:     aws.String(version),
		})
		if err != nil {
			return "", fmt.Errorf("failed to set default launch template version for %s: %w", name, err)
		}
	}

	launchTemplateCache.Lock()
	launchTemplateCache.hashes[name] = hash
	launchTemplateCache.Unlock()
	return name, nil
}

// launchTemplateData builds the launch template data for the platform,
// mirroring the fields configureInstance sets on RunInstancesInput. Instance
// type, subnet, tags and placement are deliberately absent: the fleet request
// supplies them per launch, and dedicated host (MacOS) fields never enter a
// launch template.
func (ec AWSEc2DynamicConfig) launchTemplateData() *types.RequestLaunchTemplateData {
	data := &types.RequestLaunchTemplateData{
		KeyName:      aws.String(ec.KeyName),
		ImageId:      aws.String(ec.Ami),
		EbsOptimized: aws.Bool(true),
		UserData:     ec.UserData,
		BlockDeviceMappings: []types.LaunchTemplateBlockDeviceMappingRequest{{
			DeviceName: aws.String("/dev/sda1"),
			Ebs: &types.LaunchTemplateEbsBlockDeviceRequest{
				DeleteOnTermination: aws.Bool(true),
				VolumeSize:          aws.Int32(ec.Disk),
				VolumeType:          types.VolumeTypeGp3,
				Iops:                ec.Iops,
				Throughput:          ec.Throughput,
			},
		}},
		InstanceInitiatedShutdownBehavior: types.ShutdownBehaviorTerminate,
	}
	if ec.SecurityGroup != "" {
		data.SecurityGroups = []string{ec.SecurityGroup}
	}
	if ec.SecurityGroupId != "" {
		data.SecurityGroupIds = []string{ec.SecurityGroupId}
	}
	if ec.InstanceProfileName != "" || ec.InstanceProfileArn != "" {
		instanceProfile := &types.LaunchTemplateIamInstanceProfileSpecificationRequest{}
		if ec.InstanceProfileName != "" {
			instanceProfile.Name = aws.String(ec.InstanceProfileName)
		}
		if ec.InstanceProfileArn != "" {
			instanceProfile.Arn = aws.String(ec.InstanceProfileArn)
		}
		data.IamInstanceProfile = instanceProfile
	}
	return data
}

// configureFleet builds the EC2 Fleet request for a multi-type launch: an
// instant fleet of one on-demand instance, one override per configured
// instance type — all in the single configured subnet, tried in list order via
// AllocationStrategy prioritized — and the same instance tags RunInstances
// would set, applied to both the instance and its volumes.
func (ec AWSEc2DynamicConfig) configureFleet(launchTemplateName string, taskRunName string, instanceTag string, additionalInstanceTags map[string]string) *ec2.CreateFleetInput {
	instanceTags := make([]types.Tag, 0, 3+len(additionalInstanceTags))
	instanceTags = append(instanceTags,
		types.Tag{Key: aws.String(MultiPlatformManaged), Value: aws.String("true")},
		types.Tag{Key: aws.String(cloud.InstanceTag), Value: aws.String(instanceTag)},
		types.Tag{Key: aws.String("Name"), Value: aws.String("multi-platform-builder-" + taskRunName)},
	)
	for k, v := range additionalInstanceTags {
		instanceTags = append(instanceTags, types.Tag{Key: aws.String(k), Value: aws.String(v)})
	}

	overrides := make([]types.FleetLaunchTemplateOverridesRequest, 0, len(ec.InstanceTypes))
	for i, instanceType := range ec.InstanceTypes {
		overrides = append(overrides, types.FleetLaunchTemplateOverridesRequest{
			InstanceType: types.InstanceType(instanceType),
			SubnetId:     aws.String(ec.SubnetId),
			Priority:     aws.Float64(float64(i)),
		})
	}

	return &ec2.CreateFleetInput{
		Type: types.FleetTypeInstant,
		TargetCapacitySpecification: &types.TargetCapacitySpecificationRequest{
			TotalTargetCapacity:       aws.Int32(1),
			DefaultTargetCapacityType: types.DefaultTargetCapacityTypeOnDemand,
		},
		OnDemandOptions: &types.OnDemandOptionsRequest{
			AllocationStrategy: types.FleetOnDemandAllocationStrategyPrioritized,
		},
		TagSpecifications: []types.TagSpecification{
			{ResourceType: types.ResourceTypeInstance, Tags: instanceTags},
			{ResourceType: types.ResourceTypeVolume, Tags: instanceTags},
		},
		LaunchTemplateConfigs: []types.FleetLaunchTemplateConfigRequest{{
			LaunchTemplateSpecification: &types.FleetLaunchTemplateSpecificationRequest{
				LaunchTemplateName: aws.String(launchTemplateName),
				Version:            aws.String("$Default"),
			},
			Overrides: overrides,
		}},
	}
}

// parseInstanceTypes parses the comma-separated
// dynamic.<platform>.instance-types value: entries are trimmed, a trailing
// comma is tolerated, and empty entries elsewhere are rejected. All entries
// must share one CPU architecture so a single AMI can serve the whole list.
// An empty value yields no types and the legacy single-type path applies.
func parseInstanceTypes(raw string) ([]string, error) {
	if raw == "" {
		return nil, nil
	}
	entries := strings.Split(raw, ",")
	instanceTypes := make([]string, 0, len(entries))
	for i, entry := range entries {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			if i == len(entries)-1 {
				continue
			}
			return nil, errors.New("empty instance type in the middle of the instance-types list")
		}
		instanceTypes = append(instanceTypes, entry)
	}
	for i, instanceType := range instanceTypes {
		if i > 0 && instanceTypeArchitecture(instanceType) != instanceTypeArchitecture(instanceTypes[0]) {
			return nil, fmt.Errorf("instance type %q has %s CPU architecture but %q has %s: mixing architectures is not supported",
				instanceType, instanceTypeArchitecture(instanceType), instanceTypes[0], instanceTypeArchitecture(instanceTypes[0]))
		}
	}
	return instanceTypes, nil
}

// instanceTypeArchitecture returns the CPU architecture of an EC2 instance
// type by family name. Graviton (ARM) families are enumerated by prefix; every
// other family, including unrecognized new ones, is treated as x86_64. The
// heuristic only guards the instance-types list against architecture mixes —
// AWS itself rejects an instance type that does not match the AMI.
func instanceTypeArchitecture(instanceType string) string {
	family := strings.SplitN(instanceType, ".", 2)[0]
	gravitonFamilies := []string{
		"a1", "c6g", "c7g", "c8g", "g5g", "im4gn", "is4gen",
		"m6g", "m7g", "m8g", "r6g", "r7g", "r8g", "t4g", "x2gd", "x8g",
	}
	for _, graviton := range gravitonFamilies {
		if strings.HasPrefix(family, graviton) {
			return "arm64"
		}
	}
	return "x86_64"
}

// A SecretCredentialsProvider is a collection of information needed to generate
// AWS credentials. It implements the AWS CredentialsProvider interface.
type SecretCredentialsProvider struct {
	// Name is the name of the the Kubernetes ExternalSecret resource that contains
	// the SSH key's access key ID and secret access key.
	Name string

	// Namespace is the Kubernetes namespace the ExternalSecret resource resides in.
	Namespace string

	// Client is the client (if any) to use to connect to Kubernetes.
	Client client.Client
}

// Retrieve is one of the AWS CredentialsProvider interface's methods that uses external Kubernetes
// secrets or local environment variables to generate AWS credentials.
func (r SecretCredentialsProvider) Retrieve(ctx context.Context) (aws.Credentials, error) {
	// Use local environment variables for credentials
	if r.Client == nil {
		// TODO: add a check if the ENVs are empty?
		return aws.Credentials{
			AccessKeyID:     os.Getenv("MULTI_ARCH_ACCESS_KEY"),
			SecretAccessKey: os.Getenv("MULTI_ARCH_SECRET_KEY"),
			SessionToken:    os.Getenv("MULTI_ARCH_SESSION_TOKEN"),
		}, nil
	}

	// Connect to Kubernetes to get credentials info
	s := v1.Secret{}
	nameSpacedSecret := types2.NamespacedName{Name: r.Name, Namespace: r.Namespace}
	err := r.Client.Get(ctx, nameSpacedSecret, &s)
	if err != nil {
		return aws.Credentials{},
			fmt.Errorf("failed to retrieve the secret %v from the Kubernetes client: %w", nameSpacedSecret, err)
	}

	accessKeyID := string(s.Data["access-key-id"])
	secretAccessKey := string(s.Data["secret-access-key"])
	sessionToken := ""
	if sessionTokenData, ok := s.Data["session-token"]; ok {
		sessionToken = string(sessionTokenData)
	}

	return aws.Credentials{
		AccessKeyID:     accessKeyID,
		SecretAccessKey: secretAccessKey,
		SessionToken:    sessionToken,
	}, nil
}
