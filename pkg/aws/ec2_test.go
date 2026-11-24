// Assisted-by: TAG
package aws

import (
	"encoding/base64"
	"errors"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/konflux-ci/multi-platform-controller/pkg/cloud"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	. "github.com/onsi/gomega/gstruct"
)

const systemNamespace = "multi-platform-controller"

func stringEncode(s *string) *string {
	base64val := base64.StdEncoding.EncodeToString([]byte(*s))
	return &base64val
}

var _ = Describe("Ec2 Unit Test Suit", func() {

	// Testing the provider for AwsDynamicConfig with 4 test cases:
	// 	1. A positive test for valid configuarion fields
	// 	2. A negative test with a platform name that does not exist
	//	3. A negative test with empty configuration values
	//	4. A negative test with configuration values that do not match the expected data type - non-numeric content of config string
	Describe("Testing Ec2Provider", func() {

		DescribeTable("Testing the creation of AwsDynamicConfig properly no matter the values",
			func(platformName string, testConfig map[string]string, expectedDisk int, expectedIops *int32, expectedThroughput *int32, expectedUserData *string, expectedStrictPublicAddress bool) {
				config := map[string]string{
					"dynamic." + platformName + ".region":                "test-region",
					"dynamic." + platformName + ".ami":                   "test-ami",
					"dynamic." + platformName + ".instance-type":         "test-instance-type",
					"dynamic." + platformName + ".key-name":              "test-key-name",
					"dynamic." + platformName + ".aws-secret":            "test-secret",
					"dynamic." + platformName + ".security-group":        "test-security-group",
					"dynamic." + platformName + ".security-group-id":     "test-security-group-id",
					"dynamic." + platformName + ".subnet-id":             "test-subnet-id",
					"dynamic." + platformName + ".instance-profile-name": "test-instance-profile-name",
					"dynamic." + platformName + ".instance-profile-arn":  "test-instance-profile-arn",
					"dynamic." + platformName + ".disk":                  testConfig["disk"],
					"dynamic." + platformName + ".iops":                  testConfig["iops"],
					"dynamic." + platformName + ".throughput":            testConfig["throughput"],
					"dynamic." + platformName + ".user-data":             testConfig["user-data"],
					"dynamic." + platformName + ".strict-public-address": testConfig["strict-public-address"],
				}
				provider := CreateEc2CloudConfig(platformName, config, systemNamespace)
				Expect(provider).ToNot(BeNil())
				providerConfig := provider.(AWSEc2DynamicConfig)
				Expect(providerConfig).ToNot(BeNil())

				Expect(providerConfig.Region).To(Equal("test-region"))
				Expect(providerConfig.Ami).To(Equal("test-ami"))
				Expect(providerConfig.InstanceType).To(Equal("test-instance-type"))
				Expect(providerConfig.KeyName).To(Equal("test-key-name"))
				Expect(providerConfig.Secret).To(Equal("test-secret"))
				Expect(providerConfig.SecurityGroup).To(Equal("test-security-group"))
				Expect(providerConfig.SecurityGroupId).To(Equal("test-security-group-id"))
				Expect(providerConfig.SubnetId).To(Equal("test-subnet-id"))
				Expect(providerConfig.InstanceProfileName).To(Equal("test-instance-profile-name"))
				Expect(providerConfig.InstanceProfileArn).To(Equal("test-instance-profile-arn"))
				Expect(providerConfig.Disk).To(Equal(int32(expectedDisk)))
				Expect(providerConfig.Iops).To(Equal(expectedIops))
				Expect(providerConfig.Throughput).To(Equal(expectedThroughput))
				Expect(providerConfig.UserData).Should(SatisfyAny(Equal(stringEncode(expectedUserData)), BeNil()))
				Expect(providerConfig.StrictPublicAddress).To(Equal(expectedStrictPublicAddress))
			},
			Entry("Positive - valid config map keys", "linux-largecpu-x86_64", map[string]string{
				"disk":                  "200",
				"iops":                  "100",
				"throughput":            "50",
				"user-data":             commonUserData,
				"strict-public-address": "true"},
				200, aws.Int32(100), aws.Int32(50), aws.String(commonUserData), true),
			Entry("Negative - nonexistant platform name", "koko-hazamar", map[string]string{
				"disk":                  "200",
				"iops":                  "100",
				"throughput":            "50",
				"user-data":             commonUserData,
				"strict-public-address": "false"},
				200, aws.Int32(100), aws.Int32(50), aws.String(commonUserData), false),
			Entry("Negative - missing config data", "linux-c4xlarge-arm64", map[string]string{
				"disk":                  "",
				"iops":                  "",
				"throughput":            "",
				"user-data":             "",
				"strict-public-address": ""},
				40, nil, nil, aws.String(""), false),
			Entry("Negative - config data with bad data types", "linux-g6xlarge-amd64", map[string]string{
				"disk":                  "koko-hazamar",
				"iops":                  "koko-hazamar",
				"throughput":            "koko-hazamar",
				"user-data":             commonUserData,
				"strict-public-address": "invalid"},
				40, nil, nil, aws.String(commonUserData), false),
		)
	})

	Describe("SshUser", func() {
		DescribeTable("provisioning user resolution",
			func(provisioningUser string, expected string) {
				config := AWSEc2DynamicConfig{ProvisioningUser: provisioningUser}
				Expect(config.SshUser()).Should(Equal(expected))
			},
			Entry("empty string should fall back to ec2-user", "", "ec2-user"),
			Entry("whitespace should fall back to ec2-user", "   ", "ec2-user"),
			Entry("configured user should be returned", "cloud-user", "cloud-user"),
			Entry("spaces are trimmed", " cloud-user ", "cloud-user"),
			Entry("dash prefix is removed (empty string)", "-", "ec2-user"),
			Entry("dash prefix is removed (non empty string)", "-cloud-user", "cloud-user"),
			Entry("dash and space prefixes are removed (empty string)", " -", "ec2-user"),
			Entry("space and dash prefixes are removed (empty string)", "- ", "ec2-user"),
			Entry("dash and space prefixes are removed (non empty string)", "- cloud-user", "cloud-user"),
			Entry("space and dash prefixes are removed (non empty string)", " -cloud-user", "cloud-user"),
		)
	})

	Describe("CreateEc2CloudConfig provisioning-user", func() {
		When("provisioning-user is present in the config", func() {
			It("should set ProvisioningUser on the provider", func() {
				platformName := "linux-largecpu-x86_64"
				config := map[string]string{
					"dynamic." + platformName + ".region":            "test-region",
					"dynamic." + platformName + ".ami":               "test-ami",
					"dynamic." + platformName + ".instance-type":     "test-instance-type",
					"dynamic." + platformName + ".provisioning-user": "cloud-user",
				}

				provider := CreateEc2CloudConfig(platformName, config, systemNamespace)
				providerConfig := provider.(AWSEc2DynamicConfig)

				Expect(providerConfig.ProvisioningUser).ShouldNot(BeEmpty())
				Expect(providerConfig.ProvisioningUser).Should(Equal("cloud-user"))
				Expect(providerConfig.SshUser()).Should(Equal("cloud-user"))
			})
		})

		When("provisioning-user is absent from the config", func() {
			It("should leave ProvisioningUser empty and fall back to ec2-user", func() {
				platformName := "linux-largecpu-x86_64"
				config := map[string]string{
					"dynamic." + platformName + ".region":        "test-region",
					"dynamic." + platformName + ".ami":           "test-ami",
					"dynamic." + platformName + ".instance-type": "test-instance-type",
				}

				provider := CreateEc2CloudConfig(platformName, config, systemNamespace)
				providerConfig := provider.(AWSEc2DynamicConfig)

				Expect(providerConfig.ProvisioningUser).Should(BeEmpty())
				Expect(providerConfig.SshUser()).Should(Equal("ec2-user"))
			})
		})

		When("provisioning-user is empty in the config", func() {
			It("should fall back to ec2-user", func() {
				platformName := "linux-largecpu-x86_64"
				config := map[string]string{
					"dynamic." + platformName + ".region":            "test-region",
					"dynamic." + platformName + ".ami":               "test-ami",
					"dynamic." + platformName + ".instance-type":     "test-instance-type",
					"dynamic." + platformName + ".provisioning-user": "",
				}

				provider := CreateEc2CloudConfig(platformName, config, systemNamespace)
				providerConfig := provider.(AWSEc2DynamicConfig)

				Expect(providerConfig.SshUser()).Should(Equal("ec2-user"))
			})
		})
	})

	Describe("CreateEc2CloudConfig instance-types", func() {
		baseConfig := func(platformName string) map[string]string {
			return map[string]string{
				"dynamic." + platformName + ".region":        "test-region",
				"dynamic." + platformName + ".ami":           "test-ami",
				"dynamic." + platformName + ".instance-type": "c5.xlarge",
				"dynamic." + platformName + ".key-name":      "test-key",
				"dynamic." + platformName + ".subnet-id":     "subnet-1",
			}
		}

		DescribeTable("parsing the instance-types key",
			func(instanceTypesValue string, expectedTypes []string, expectedInstanceType string) {
				platformName := "linux-amd64"
				config := baseConfig(platformName)
				config["dynamic."+platformName+".instance-types"] = instanceTypesValue
				provider := CreateEc2CloudConfig(platformName, config, systemNamespace).(AWSEc2DynamicConfig)

				Expect(provider.InstanceTypes).Should(Equal(expectedTypes))
				Expect(provider.InstanceType).Should(Equal(expectedInstanceType))
				Expect(provider.instanceTypesErr).ShouldNot(HaveOccurred())
				Expect(provider.platformName).Should(Equal(platformName))
			},
			Entry("empty value keeps the single instance-type value", "", nil, "c5.xlarge"),
			Entry("single entry", "t3.micro", []string{"t3.micro"}, "t3.micro"),
			Entry("multiple entries", "inf1.24xlarge,t3.micro,c5.xlarge", []string{"inf1.24xlarge", "t3.micro", "c5.xlarge"}, "inf1.24xlarge"),
			Entry("entries are trimmed", " inf1.24xlarge , t3.micro ", []string{"inf1.24xlarge", "t3.micro"}, "inf1.24xlarge"),
			Entry("trailing comma is tolerated", "inf1.24xlarge,t3.micro,", []string{"inf1.24xlarge", "t3.micro"}, "inf1.24xlarge"),
			Entry("arm64 families of several generations", "t4g.medium,m6g.large,x8g.48xlarge", []string{"t4g.medium", "m6g.large", "x8g.48xlarge"}, "t4g.medium"),
		)

		When("the instance-types key is absent", func() {
			It("should keep the single instance-type value", func() {
				platformName := "linux-amd64"
				provider := CreateEc2CloudConfig(platformName, baseConfig(platformName), systemNamespace).(AWSEc2DynamicConfig)

				Expect(provider.InstanceTypes).Should(BeNil())
				Expect(provider.InstanceType).Should(Equal("c5.xlarge"))
				Expect(provider.instanceTypesErr).ShouldNot(HaveOccurred())
				Expect(provider.platformName).Should(Equal(platformName))
			})
		})

		DescribeTable("rejecting malformed instance-types values",
			func(instanceTypesValue string) {
				platformName := "linux-amd64"
				config := baseConfig(platformName)
				config["dynamic."+platformName+".instance-types"] = instanceTypesValue
				provider := CreateEc2CloudConfig(platformName, config, systemNamespace).(AWSEc2DynamicConfig)

				Expect(provider.InstanceTypes).Should(BeNil())
				Expect(provider.instanceTypesErr).Should(HaveOccurred())
			},
			Entry("empty entry in the middle", "inf1.24xlarge,,t3.micro"),
			Entry("leading comma", ",inf1.24xlarge"),
			Entry("comma only", ","),
			Entry("architecture mix", "t3.micro,t4g.micro"),
			Entry("architecture mix across families", "c5.xlarge,m6g.large,t3.micro"),
		)

		When("the instance-types value is malformed", func() {
			It("should fail the launch with the parse error", func(ctx SpecContext) {
				platformName := "linux-amd64"
				config := map[string]string{
					"dynamic." + platformName + ".instance-types": "inf1.24xlarge,,t3.micro",
				}
				provider := CreateEc2CloudConfig(platformName, config, systemNamespace).(AWSEc2DynamicConfig)
				provider.ec2Client = &mockEC2Client{}

				_, err := provider.LaunchInstance(nil, ctx, "ns:task", "tag", map[string]string{})

				Expect(err).Should(MatchError(ContainSubstring("invalid instance-types configuration")))
				Expect(err).Should(MatchError(ContainSubstring("empty instance type")))
			})
		})

		When("the instance-types value mixes CPU architectures", func() {
			It("should fail the launch with an architecture error", func(ctx SpecContext) {
				platformName := "linux-amd64"
				config := map[string]string{
					"dynamic." + platformName + ".instance-types": "t3.micro,t4g.micro",
				}
				provider := CreateEc2CloudConfig(platformName, config, systemNamespace).(AWSEc2DynamicConfig)
				provider.ec2Client = &mockEC2Client{}

				_, err := provider.LaunchInstance(nil, ctx, "ns:task", "tag", map[string]string{})

				Expect(err).Should(MatchError(ContainSubstring("invalid instance-types configuration")))
				Expect(err).Should(MatchError(ContainSubstring("mixing architectures is not supported")))
			})
		})
	})

	Describe("CloudProvider methods", func() {
		var (
			mock *mockEC2Client
			cfg  AWSEc2DynamicConfig
		)

		BeforeEach(func() {
			mock = &mockEC2Client{}
			cfg = AWSEc2DynamicConfig{
				InstanceType: "t4g.medium",
				KeyName:      "test-key",
				Ami:          "ami-123",
				Disk:         40,
				ec2Client:    mock,
			}
		})

		Describe("LaunchInstance", func() {
			When("the EC2 API returns an instance", func() {
				It("should return the instance ID", func(ctx SpecContext) {
					mock.RunInstancesOutput = &ec2.RunInstancesOutput{
						Instances: []types.Instance{{InstanceId: aws.String("i-abc123")}},
					}

					id, err := cfg.LaunchInstance(nil, ctx, "ns:task", "tag", map[string]string{})

					Expect(err).ShouldNot(HaveOccurred())
					Expect(string(id)).Should(Equal("i-abc123"))
				})
			})

			When("the EC2 API returns an error", func() {
				It("should return the error", func(ctx SpecContext) {
					mock.RunInstancesErr = errors.New("ec2 launch failed")

					_, err := cfg.LaunchInstance(nil, ctx, "ns:task", "tag", map[string]string{})

					Expect(err).Should(MatchError(ContainSubstring("failed to launch EC2 instance")))
				})
			})

			When("the EC2 API returns zero instances", func() {
				It("should return a 'no instances created' error", func(ctx SpecContext) {
					mock.RunInstancesOutput = &ec2.RunInstancesOutput{Instances: []types.Instance{}}

					_, err := cfg.LaunchInstance(nil, ctx, "ns:task", "tag", map[string]string{})

					Expect(err).Should(MatchError("no EC2 instances were created"))
				})
			})

			When("the TaskRun ID is invalid", func() {
				It("should return a validation error", func(ctx SpecContext) {
					_, err := cfg.LaunchInstance(nil, ctx, "invalid-no-colon", "tag", map[string]string{})

					Expect(err).Should(MatchError(ContainSubstring("invalid TaskRun ID")))
				})
			})

			When("the platform lists multiple instance types", func() {
				var fleetCfg AWSEc2DynamicConfig

				BeforeEach(func() {
					resetLaunchTemplateCache()
					fleetCfg = AWSEc2DynamicConfig{
						InstanceTypes: []string{"inf1.24xlarge", "t3.micro"},
						InstanceType:  "inf1.24xlarge",
						KeyName:       "test-key",
						Ami:           "ami-123",
						Disk:          40,
						SubnetId:      "subnet-1",
						platformName:  "linux-amd64",
						ec2Client:     mock,
					}
					mock.DescribeLaunchTemplatesOutput = &ec2.DescribeLaunchTemplatesOutput{}
					mock.CreateFleetOutput = &ec2.CreateFleetOutput{
						Instances: []types.CreateFleetInstance{{
							InstanceIds:  []string{"i-fleet"},
							InstanceType: "t3.micro",
						}},
					}
				})

				It("should launch through CreateFleet and return the instance ID", func(ctx SpecContext) {
					id, err := fleetCfg.LaunchInstance(nil, ctx, "ns:task", "tag", map[string]string{})

					Expect(err).ShouldNot(HaveOccurred())
					Expect(string(id)).Should(Equal("i-fleet"))
					Expect(mock.RunInstancesInput).Should(BeNil())
					Expect(mock.CreateFleetInput).ShouldNot(BeNil())
					Expect(mock.CreateLaunchTemplateInput).ShouldNot(BeNil())
				})

				It("should build an instant one-instance on-demand fleet with one prioritized override per type", func(ctx SpecContext) {
					_, err := fleetCfg.LaunchInstance(nil, ctx, "ns:task", "tag", map[string]string{})
					Expect(err).ShouldNot(HaveOccurred())

					input := mock.CreateFleetInput
					Expect(input.Type).Should(Equal(types.FleetTypeInstant))
					Expect(input.TargetCapacitySpecification).ShouldNot(BeNil())
					Expect(*input.TargetCapacitySpecification.TotalTargetCapacity).Should(Equal(int32(1)))
					Expect(input.TargetCapacitySpecification.DefaultTargetCapacityType).Should(Equal(types.DefaultTargetCapacityTypeOnDemand))
					Expect(input.OnDemandOptions).ShouldNot(BeNil())
					Expect(input.OnDemandOptions.AllocationStrategy).Should(Equal(types.FleetOnDemandAllocationStrategyPrioritized))

					Expect(input.LaunchTemplateConfigs).Should(HaveLen(1))
					templateConfig := input.LaunchTemplateConfigs[0]
					Expect(templateConfig.LaunchTemplateSpecification).ShouldNot(BeNil())
					Expect(*templateConfig.LaunchTemplateSpecification.LaunchTemplateName).Should(Equal("mpc-linux-amd64"))
					Expect(*templateConfig.LaunchTemplateSpecification.Version).Should(Equal("$Default"))

					overrides := templateConfig.Overrides
					Expect(overrides).Should(HaveLen(2))
					Expect(string(overrides[0].InstanceType)).Should(Equal("inf1.24xlarge"))
					Expect(*overrides[0].SubnetId).Should(Equal("subnet-1"))
					Expect(*overrides[0].Priority).Should(Equal(0.0))
					Expect(string(overrides[1].InstanceType)).Should(Equal("t3.micro"))
					Expect(*overrides[1].SubnetId).Should(Equal("subnet-1"))
					Expect(*overrides[1].Priority).Should(Equal(1.0))
				})

				It("should tag both the instance and its volumes like RunInstances does", func(ctx SpecContext) {
					_, err := fleetCfg.LaunchInstance(nil, ctx, "ns:task", "tag", map[string]string{})
					Expect(err).ShouldNot(HaveOccurred())

					Expect(mock.CreateFleetInput.TagSpecifications).Should(HaveLen(2))
					Expect(mock.CreateFleetInput.TagSpecifications[0].ResourceType).Should(Equal(types.ResourceTypeInstance))
					Expect(mock.CreateFleetInput.TagSpecifications[1].ResourceType).Should(Equal(types.ResourceTypeVolume))
					for _, tagSpecification := range mock.CreateFleetInput.TagSpecifications {
						tags := tagSpecification.Tags
						Expect(tags).Should(ContainElement(SatisfyAll(
							HaveField("Key", PointTo(Equal(MultiPlatformManaged))),
							HaveField("Value", PointTo(Equal("true"))))))
						Expect(tags).Should(ContainElement(SatisfyAll(
							HaveField("Key", PointTo(Equal(cloud.InstanceTag))),
							HaveField("Value", PointTo(Equal("tag"))))))
						Expect(tags).Should(ContainElement(SatisfyAll(
							HaveField("Key", PointTo(Equal("Name"))),
							HaveField("Value", PointTo(Equal("multi-platform-builder-task"))))))
						Expect(tags).Should(ContainElement(SatisfyAll(
							HaveField("Key", PointTo(Equal(cloud.TaskRunTagKey))),
							HaveField("Value", PointTo(Equal("ns:task"))))))
					}
				})

				When("the fleet reports per-pool errors alongside the launched instance", func() {
					It("should still return the instance ID", func(ctx SpecContext) {
						mock.CreateFleetOutput = &ec2.CreateFleetOutput{
							Instances: []types.CreateFleetInstance{{InstanceIds: []string{"i-fleet"}, InstanceType: "t3.micro"}},
							Errors: []types.CreateFleetError{{
								ErrorCode:    aws.String("InstanceLimitExceeded"),
								ErrorMessage: aws.String("inf1.24xlarge exceeds the vCPU quota"),
							}},
						}

						id, err := fleetCfg.LaunchInstance(nil, ctx, "ns:task", "tag", map[string]string{})

						Expect(err).ShouldNot(HaveOccurred())
						Expect(string(id)).Should(Equal("i-fleet"))
					})
				})

				When("the fleet cannot launch any instance", func() {
					It("should return an error listing every per-pool error", func(ctx SpecContext) {
						mock.CreateFleetOutput = &ec2.CreateFleetOutput{
							Errors: []types.CreateFleetError{
								{ErrorCode: aws.String("InstanceLimitExceeded"), ErrorMessage: aws.String("inf1.24xlarge exceeds the vCPU quota")},
								{ErrorCode: aws.String("InsufficientInstanceCapacity"), ErrorMessage: aws.String("t3.micro has no capacity")},
							},
						}

						_, err := fleetCfg.LaunchInstance(nil, ctx, "ns:task", "tag", map[string]string{})

						Expect(err).Should(MatchError(ContainSubstring("no instances were created")))
						Expect(err).Should(MatchError(ContainSubstring("InstanceLimitExceeded: inf1.24xlarge exceeds the vCPU quota")))
						Expect(err).Should(MatchError(ContainSubstring("InsufficientInstanceCapacity: t3.micro has no capacity")))
					})

					It("should report when the fleet returns no error details", func(ctx SpecContext) {
						mock.CreateFleetOutput = &ec2.CreateFleetOutput{}

						_, err := fleetCfg.LaunchInstance(nil, ctx, "ns:task", "tag", map[string]string{})

						Expect(err).Should(MatchError(ContainSubstring("no error details were returned")))
					})
				})

				When("the CreateFleet API call fails", func() {
					It("should return the error", func(ctx SpecContext) {
						mock.CreateFleetErr = errors.New("api failure")

						_, err := fleetCfg.LaunchInstance(nil, ctx, "ns:task", "tag", map[string]string{})

						Expect(err).Should(MatchError(ContainSubstring("failed to launch EC2 fleet")))
						Expect(err).Should(MatchError(ContainSubstring("api failure")))
					})
				})

				When("the launch template cannot be ensured", func() {
					It("should return the ensure error", func(ctx SpecContext) {
						mock.DescribeLaunchTemplatesErr = errors.New("AccessDenied")

						_, err := fleetCfg.LaunchInstance(nil, ctx, "ns:task", "tag", map[string]string{})

						Expect(err).Should(MatchError(ContainSubstring("failed to ensure launch template")))
						Expect(err).Should(MatchError(ContainSubstring("AccessDenied")))
					})
				})

				When("the platform is configured for MacOS dedicated hosts", func() {
					It("should reject the fleet launch", func(ctx SpecContext) {
						fleetCfg.Tenancy = "host"
						fleetCfg.HostResourceGroupArn = "arn:aws:resource-groups:us-east-1:123456789012:group/mac-hosts"
						fleetCfg.LicenseConfigurationArn = "arn:aws:license-manager:us-east-1:123456789012:license-configuration:lic-macos"

						_, err := fleetCfg.LaunchInstance(nil, ctx, "ns:task", "tag", map[string]string{})

						Expect(err).Should(MatchError(ContainSubstring("MacOS dedicated host")))
						Expect(mock.CreateFleetInput).Should(BeNil())
					})
				})

				When("no subnet is configured", func() {
					It("should reject the fleet launch", func(ctx SpecContext) {
						fleetCfg.SubnetId = ""

						_, err := fleetCfg.LaunchInstance(nil, ctx, "ns:task", "tag", map[string]string{})

						Expect(err).Should(MatchError(ContainSubstring("subnet-id")))
						Expect(mock.CreateFleetInput).Should(BeNil())
					})
				})
			})

			When("the platform lists a single instance type", func() {
				It("should launch through RunInstances with that type", func(ctx SpecContext) {
					platformName := "linux-amd64"
					config := map[string]string{
						"dynamic." + platformName + ".instance-types": "m5.large",
						"dynamic." + platformName + ".instance-type":  "c5.xlarge",
						"dynamic." + platformName + ".key-name":       "test-key",
					}
					provider := CreateEc2CloudConfig(platformName, config, systemNamespace).(AWSEc2DynamicConfig)
					provider.ec2Client = mock
					mock.RunInstancesOutput = &ec2.RunInstancesOutput{
						Instances: []types.Instance{{InstanceId: aws.String("i-single")}},
					}

					id, err := provider.LaunchInstance(nil, ctx, "ns:task", "tag", map[string]string{})

					Expect(err).ShouldNot(HaveOccurred())
					Expect(string(id)).Should(Equal("i-single"))
					Expect(mock.RunInstancesInput).ShouldNot(BeNil())
					Expect(mock.RunInstancesInput.InstanceType).Should(Equal(types.InstanceType("m5.large")))
					Expect(mock.CreateFleetInput).Should(BeNil())
				})
			})

			When("the platform has no instance-types configured", func() {
				It("should launch through RunInstances without calling CreateFleet", func(ctx SpecContext) {
					mock.RunInstancesOutput = &ec2.RunInstancesOutput{
						Instances: []types.Instance{{InstanceId: aws.String("i-legacy")}},
					}

					id, err := cfg.LaunchInstance(nil, ctx, "ns:task", "tag", map[string]string{})

					Expect(err).ShouldNot(HaveOccurred())
					Expect(string(id)).Should(Equal("i-legacy"))
					Expect(mock.RunInstancesInput).ShouldNot(BeNil())
					Expect(mock.RunInstancesInput.InstanceType).Should(Equal(types.InstanceType("t4g.medium")))
					Expect(mock.RunInstancesInput.MinCount).Should(PointTo(Equal(int32(1))))
					Expect(mock.RunInstancesInput.MaxCount).Should(PointTo(Equal(int32(1))))
					Expect(mock.CreateFleetInput).Should(BeNil())
				})
			})
		})

		Describe("CountInstances", func() {
			// Each entry provides the full set of instances and the instanceTag,
			// verifying the tag filters sent to DescribeInstances and the
			// state-based filtering. Managed instances are scoped by tags; a
			// platform may launch several instance types, so any instance type
			// counts as long as the tags match.
			DescribeTable("tag filters and instance filtering",
				func(ctx SpecContext, instanceTag string, instances []types.Instance, expectedCount int) {
					mock.DescribeInstancesOutput = &ec2.DescribeInstancesOutput{
						Reservations: []types.Reservation{{Instances: instances}},
					}

					count, err := cfg.CountInstances(nil, ctx, instanceTag)

					Expect(err).ShouldNot(HaveOccurred())
					Expect(count).Should(Equal(expectedCount))

					Expect(mock.DescribeInstancesInput.Filters).Should(HaveLen(2))
					Expect(*mock.DescribeInstancesInput.Filters[0].Name).Should(Equal("tag:multi-platform-instance"))
					Expect(mock.DescribeInstancesInput.Filters[0].Values).Should(ConsistOf(instanceTag))
					Expect(*mock.DescribeInstancesInput.Filters[1].Name).Should(Equal("tag:MultiPlatformManaged"))
					Expect(mock.DescribeInstancesInput.Filters[1].Values).Should(ConsistOf("true"))
				},
				Entry("running + configured type = counted",
					"my-tag",
					[]types.Instance{
						{InstanceId: aws.String("i-1"), State: &types.InstanceState{Name: types.InstanceStateNameRunning}, InstanceType: "t4g.medium"},
					}, 1,
				),
				Entry("running + configured type alongside terminated + configured type = only running counted",
					"prod-tag",
					[]types.Instance{
						{InstanceId: aws.String("i-1"), State: &types.InstanceState{Name: types.InstanceStateNameRunning}, InstanceType: "t4g.medium"},
						{InstanceId: aws.String("i-2"), State: &types.InstanceState{Name: types.InstanceStateNameTerminated}, InstanceType: "t4g.medium"},
					}, 1,
				),
				Entry("running + other instance type = counted (tags scope managed instances)",
					"my-tag",
					[]types.Instance{
						{InstanceId: aws.String("i-1"), State: &types.InstanceState{Name: types.InstanceStateNameRunning}, InstanceType: "m5.large"},
					}, 1,
				),
				Entry("running instances of several types = all counted (fleet fallback types)",
					"my-tag",
					[]types.Instance{
						{InstanceId: aws.String("i-1"), State: &types.InstanceState{Name: types.InstanceStateNameRunning}, InstanceType: "t4g.medium"},
						{InstanceId: aws.String("i-2"), State: &types.InstanceState{Name: types.InstanceStateNameRunning}, InstanceType: "m5.large"},
					}, 2,
				),
				Entry("pending + configured type = counted (non-terminated states count)",
					"my-tag",
					[]types.Instance{
						{InstanceId: aws.String("i-1"), State: &types.InstanceState{Name: types.InstanceStateNamePending}, InstanceType: "t4g.medium"},
					}, 1,
				),
				Entry("stopped + configured type = counted",
					"my-tag",
					[]types.Instance{
						{InstanceId: aws.String("i-1"), State: &types.InstanceState{Name: types.InstanceStateNameStopped}, InstanceType: "t4g.medium"},
					}, 1,
				),
				Entry("two matching instances across different tags = both counted",
					"multi-tag",
					[]types.Instance{
						{InstanceId: aws.String("i-1"), State: &types.InstanceState{Name: types.InstanceStateNameRunning}, InstanceType: "t4g.medium"},
						{InstanceId: aws.String("i-2"), State: &types.InstanceState{Name: types.InstanceStateNameRunning}, InstanceType: "t4g.medium"},
					}, 2,
				),
				Entry("empty reservations = zero count",
					"my-tag",
					[]types.Instance{}, 0,
				),
			)

			When("the EC2 API returns an error", func() {
				It("should return -1 and the error", func(ctx SpecContext) {
					mock.DescribeInstancesErr = errors.New("api failure")

					count, err := cfg.CountInstances(nil, ctx, "tag")

					Expect(err).Should(MatchError(ContainSubstring("failed to retrieve EC2 instances")))
					Expect(count).Should(Equal(-1))
				})
			})
		})

		Describe("TerminateInstance", func() {
			When("the EC2 API succeeds", func() {
				It("should return nil", func(ctx SpecContext) {
					Expect(cfg.TerminateInstance(nil, ctx, "i-123")).ShouldNot(HaveOccurred())
				})
			})

			When("the EC2 API fails", func() {
				It("should return the error", func(ctx SpecContext) {
					mock.TerminateInstancesErr = errors.New("terminate failed")

					Expect(cfg.TerminateInstance(nil, ctx, "i-123")).Should(MatchError("terminate failed"))
				})
			})
		})

		Describe("GetInstanceAddress", func() {
			When("DescribeInstances returns an error", func() {
				It("should return empty string without error (transient)", func(ctx SpecContext) {
					mock.DescribeInstancesErr = errors.New("api error")

					addr, err := cfg.GetInstanceAddress(nil, ctx, "i-123")

					Expect(err).ShouldNot(HaveOccurred())
					Expect(addr).Should(BeEmpty())
				})
			})

			When("no reservations are returned", func() {
				It("should return empty string", func(ctx SpecContext) {
					mock.DescribeInstancesOutput = &ec2.DescribeInstancesOutput{}

					addr, err := cfg.GetInstanceAddress(nil, ctx, "i-123")

					Expect(err).ShouldNot(HaveOccurred())
					Expect(addr).Should(BeEmpty())
				})
			})
		})

		Describe("GetState", func() {
			DescribeTable("instance state mapping",
				func(ctx SpecContext, stateName types.InstanceStateName) {
					mock.DescribeInstancesOutput = &ec2.DescribeInstancesOutput{
						Reservations: []types.Reservation{{
							Instances: []types.Instance{{
								State: &types.InstanceState{Name: stateName},
							}},
						}},
					}

					state, err := cfg.GetState(nil, ctx, "i-123")

					Expect(err).ShouldNot(HaveOccurred())
					Expect(state).Should(Equal(cloud.OKState))
				},
				Entry("running instance should return OKState", types.InstanceStateNameRunning),
				Entry("pending instance should return OKState", types.InstanceStateNamePending),
				Entry("terminated instance should return OKState", types.InstanceStateNameTerminated),
				Entry("stopped instance should return OKState", types.InstanceStateNameStopped),
				Entry("shutting-down instance should return OKState", types.InstanceStateNameShuttingDown),
				Entry("stopping instance should return OKState", types.InstanceStateNameStopping),
			)

			When("DescribeInstances returns an error", func() {
				It("should return empty state without error (transient)", func(ctx SpecContext) {
					mock.DescribeInstancesErr = errors.New("api error")

					state, err := cfg.GetState(nil, ctx, "i-123")

					Expect(err).ShouldNot(HaveOccurred())
					Expect(state).Should(BeEmpty())
				})
			})

			When("no reservations are returned", func() {
				It("should return FailedState", func(ctx SpecContext) {
					mock.DescribeInstancesOutput = &ec2.DescribeInstancesOutput{}

					state, err := cfg.GetState(nil, ctx, "i-123")

					Expect(err).ShouldNot(HaveOccurred())
					Expect(state).Should(Equal(cloud.FailedState))
				})
			})
		})

		Describe("ListInstances", func() {
			When("the EC2 API returns an error", func() {
				It("should return the error", func(ctx SpecContext) {
					mock.DescribeInstancesErr = errors.New("api failure")

					_, err := cfg.ListInstances(nil, ctx, "tag")

					Expect(err).Should(MatchError(ContainSubstring("failed to retrieve EC2 instances")))
				})
			})

			When("no instances match", func() {
				It("should return an empty list", func(ctx SpecContext) {
					mock.DescribeInstancesOutput = &ec2.DescribeInstancesOutput{}

					instances, err := cfg.ListInstances(nil, ctx, "tag")

					Expect(err).ShouldNot(HaveOccurred())
					Expect(instances).Should(BeEmpty())
				})
			})

			When("instances match the tag and state filters", func() {
				It("should return only non-terminated reachable instances of any type", func(ctx SpecContext) {
					cfg.pingFunc = func(_ string) error { return nil }
					now := time.Now()
					mock.DescribeInstancesOutput = &ec2.DescribeInstancesOutput{
						Reservations: []types.Reservation{{
							Instances: []types.Instance{
								{InstanceId: aws.String("i-match"), State: &types.InstanceState{Name: types.InstanceStateNameRunning}, InstanceType: "t4g.medium", PublicIpAddress: aws.String("10.0.0.1"), LaunchTime: &now},
								{InstanceId: aws.String("i-terminated"), State: &types.InstanceState{Name: types.InstanceStateNameTerminated}, InstanceType: "t4g.medium", PublicIpAddress: aws.String("10.0.0.2"), LaunchTime: &now},
								{InstanceId: aws.String("i-fallback-type"), State: &types.InstanceState{Name: types.InstanceStateNameRunning}, InstanceType: "m5.large", PublicIpAddress: aws.String("10.0.0.3"), LaunchTime: &now},
							},
						}},
					}

					instances, err := cfg.ListInstances(nil, ctx, "tag")

					Expect(err).ShouldNot(HaveOccurred())
					Expect(instances).Should(HaveLen(2))
					instanceIDs := []string{string(instances[0].InstanceId), string(instances[1].InstanceId)}
					Expect(instanceIDs).Should(ConsistOf("i-match", "i-fallback-type"))
					Expect(instances[0].Address).ShouldNot(BeEmpty())
					Expect(instances[0].StartTime).Should(BeTemporally("~", now, time.Second))
				})
			})
		})
	})
})

var commonUserData = `|-
Content-Type: multipart/mixed; boundary="//"
MIME-Version: 1.0

--//
Content-Type: text/cloud-config; charset="us-ascii"
MIME-Version: 1.0
Content-Transfer-Encoding: 7bit
Content-Disposition: attachment; filename="cloud-config.txt"

#cloud-config
cloud_final_modules:
  - [scripts-user, always]

--//
Content-Type: text/x-shellscript; charset="us-ascii"
MIME-Version: 1.0
Content-Transfer-Encoding: 7bit
Content-Disposition: attachment; filename="userdata.txt"

#!/bin/bash -ex

if lsblk -no FSTYPE /dev/nvme1n1 | grep -qE "\S"; then
 echo "File system exists on the disk."
else
 echo "No file system found on the disk /dev/nvme1n1"
 mkfs -t xfs /dev/nvme1n1
fi

mount /dev/nvme1n1 /home

if [ -d "/home/var-lib-containers" ]; then
 echo "Directory "/home/var-lib-containers" exist"
else
 echo "Directory "/home/var-lib-containers" doesn|t exist"
 mkdir -p /home/var-lib-containers /var/lib/containers
fi

mount --bind /home/var-lib-containers /var/lib/containers

if [ -d "/home/ec2-user" ]; then
echo "ec2-user home exists"
else
echo "ec2-user home doesnt exist"
mkdir -p /home/ec2-user/.ssh
chown -R ec2-user /home/ec2-user
fi

sed -n "s,.*\(ssh-.*\s\),\1,p" /root/.ssh/authorized_keys > /home/ec2-user/.ssh/authorized_keys
chown ec2-user /home/ec2-user/.ssh/authorized_keys
chmod 600 /home/ec2-user/.ssh/authorized_keys
chmod 700 /home/ec2-user/.ssh
restorecon -r /home/ec2-user

--//--`
