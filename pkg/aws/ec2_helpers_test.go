package aws

import (
	"encoding/base64"
	"errors"

	"github.com/konflux-ci/multi-platform-controller/pkg/cloud"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	. "github.com/onsi/gomega/gstruct"

	"github.com/aws/aws-sdk-go-v2/aws"
	ec2 "github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/aws-sdk-go-v2/service/ec2/types"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

var _ = Describe("AWS EC2 Helper Functions", func() {
	Describe("validateIPAddress", func() {
		ecConfig := AWSEc2DynamicConfig{}

		It("should return error when instance has no IP addresses or DNS name", func(ctx SpecContext) {
			instance := &types.Instance{
				InstanceId:       aws.String("koko_hazamar"),
				PrivateIpAddress: nil,
				PublicIpAddress:  nil,
				PublicDnsName:    nil,
			}

			ip, err := ecConfig.validateIPAddress(ctx, instance)

			Expect(err).Should(HaveOccurred())
			Expect(err.Error()).Should(ContainSubstring("has no accessible IP address"))
			Expect(err.Error()).Should(ContainSubstring("koko_hazamar"))
			Expect(ip).Should(BeEmpty())
		})
	})

	// This test is only here to check AWS connectivity in a very primitive and quick way until assertion testing
	// work starts
	Describe("Testing pingIpAddress", func() {
		DescribeTable("Testing the ability to ping a remote AWS ec2 instance via SSH",
			func(testInstanceIP string, shouldFail bool) {

				err := pingIPAddress(testInstanceIP)

				if !shouldFail {
					Expect(err).ShouldNot(HaveOccurred())
				} else {
					Expect(err).Should(HaveOccurred())
				}

			},
			Entry("Positive test - IP address", "169.48.19.34", false),
			Entry("Negative test - no such IP address", "192.168.4.231", true),
			Entry("Negative test - no such DNS name", "not a DNS name, that's for sure", true),
			Entry("Negative test - not an IP address", "Not an IP address", true),
		)
	})
	// A unit test for configureInstance. Bypasses CreateEc2CloudConfig's logic to isolate cases where its logic may be
	// faulty, hence the helper function newDefaultValidEC2ConfigForInstance which provides a neutral all-good
	// AWSEc2DynamicConfig with baseline and passing generic configurations.
	// Breaks the various configuration fields to families of configuration topics, each tested in it own Context. Some
	// have happy test paths, some have sad ones.
	// Assisted-by: Gemini
	Describe("Testing configureInstance", func() {
		var (
			ecConfig       AWSEc2DynamicConfig
			taskRunName    string
			instanceTag    string
			additionalTags map[string]string
		)

		BeforeEach(func() {
			taskRunName = "cfg-taskrun"
			instanceTag = "cfg-instance"
			additionalTags = make(map[string]string)
			ecConfig = newDefaultValidEC2ConfigForInstance()
		})

		When("configuring basic instance details", func() {
			It("should correctly set KeyName, AMI, and InstanceType", func() {
				runInput, err := ecConfig.configureInstance(taskRunName, instanceTag, additionalTags)
				Expect(err).ShouldNot(HaveOccurred())
				Expect(runInput.KeyName).To(Equal(&ecConfig.KeyName))
				Expect(runInput.ImageId).To(Equal(&ecConfig.Ami))
				Expect(runInput.InstanceType).To(Equal(types.InstanceType(ecConfig.InstanceType)))
			})

			It("should set MinCount, MaxCount, EbsOptimized, ShutdownBehavior with valid and default properties",
				func() {
					runInput, err := ecConfig.configureInstance(taskRunName, instanceTag, additionalTags)
					Expect(err).ShouldNot(HaveOccurred())
					By("Verifying the current specific values")
					Expect(runInput.MinCount).To(Equal(aws.Int32(1)))
					Expect(runInput.MaxCount).To(Equal(aws.Int32(1)))
					Expect(runInput.EbsOptimized).To(PointTo(BeTrue()))
					Expect(runInput.InstanceInitiatedShutdownBehavior).To(Equal(types.ShutdownBehaviorTerminate))

					By("verifying properties of instance counts")
					Expect(runInput.MinCount).NotTo(BeNil())
					Expect(runInput.MaxCount).NotTo(BeNil())
					Expect(*runInput.MinCount).To(BeNumerically(">", 0),
						"MinCount should be positive")
					Expect(*runInput.MaxCount).To(BeNumerically(">=", *runInput.MinCount),
						"MaxCount should be greater than or equal to MinCount")
				})
		})

		When("configuring networking", func() {
			DescribeTable("with various subnet and security group settings",
				// This DescribeTable takes an ecConfig-mutating function and a function that verifies the new content
				// of the ecConfig after going through configureInstance
				func(setupConfig func() AWSEc2DynamicConfig, verify func(input *ec2.RunInstancesInput)) {
					currentConfig := setupConfig()
					runInput, _ := currentConfig.configureInstance(taskRunName, instanceTag, additionalTags)
					verify(runInput)
				},
				Entry("using baseline SubnetId and SecurityGroupId",
					func() AWSEc2DynamicConfig { return newDefaultValidEC2ConfigForInstance() },
					func(input *ec2.RunInstancesInput) {
						Expect(input.SubnetId).To(Equal(&ecConfig.SubnetId))
						Expect(input.SecurityGroupIds).To(ContainElement(ecConfig.SecurityGroupId))
						Expect(input.SecurityGroups).To(BeNil())
					},
				),
				Entry("with only SecurityGroup name (clearing SG ID from baseline)",
					func() AWSEc2DynamicConfig {
						conf := newDefaultValidEC2ConfigForInstance()
						conf.SecurityGroup = "sg-name-example"
						conf.SecurityGroupId = ""
						return conf
					},
					func(input *ec2.RunInstancesInput) {
						Expect(input.SecurityGroups).To(ContainElement("sg-name-example"))
						Expect(input.SecurityGroupIds).To(BeNil())
					},
				),
				Entry("with no network settings (clearing baseline)",
					func() AWSEc2DynamicConfig {
						conf := newDefaultValidEC2ConfigForInstance()
						conf.SubnetId = ""
						conf.SecurityGroupId = ""
						conf.SecurityGroup = ""
						return conf
					},
					func(input *ec2.RunInstancesInput) {
						Expect(input.SubnetId).To(BeNil())
						Expect(input.SecurityGroups).To(BeNil())
						Expect(input.SecurityGroupIds).To(BeNil())
					},
				),
			)
		})

		When("configuring IAM instance profile", func() {
			DescribeTable("with various IAM profile settings",
				// This DescribeTable also takes an ecConfig-mutating function and a function that verifies the new
				//content of the ecConfig after going through configureInstance
				func(setupConfig func() AWSEc2DynamicConfig, verify func(input *ec2.RunInstancesInput)) {
					currentConfig := setupConfig()
					runInput, _ := currentConfig.configureInstance(taskRunName, instanceTag, additionalTags)
					verify(runInput)
				},
				Entry("using baseline InstanceProfileArn",
					func() AWSEc2DynamicConfig { return newDefaultValidEC2ConfigForInstance() },
					func(input *ec2.RunInstancesInput) {
						Expect(input.IamInstanceProfile).NotTo(BeNil())
						Expect(input.IamInstanceProfile.Arn).To(Equal(&ecConfig.InstanceProfileArn))
						Expect(input.IamInstanceProfile.Name).To(BeNil())
					},
				),
				Entry("with only InstanceProfileName (clearing ARN from baseline)",
					func() AWSEc2DynamicConfig {
						conf := newDefaultValidEC2ConfigForInstance()
						conf.InstanceProfileName = "profile-name-example"
						conf.InstanceProfileArn = ""
						return conf
					},
					func(input *ec2.RunInstancesInput) {
						Expect(input.IamInstanceProfile).NotTo(BeNil())
						Expect(input.IamInstanceProfile.Name).To(PointTo(Equal("profile-name-example")))
						Expect(input.IamInstanceProfile.Arn).To(BeNil())
					},
				),
				// TODO: scenarios like missing AMI fields need to be tested properly. Not in the scope of this humble unit test
				Entry("with no IAM profile settings (clearing baseline)",
					func() AWSEc2DynamicConfig {
						conf := newDefaultValidEC2ConfigForInstance()
						conf.InstanceProfileName = ""
						conf.InstanceProfileArn = ""
						return conf
					},
					func(input *ec2.RunInstancesInput) { Expect(input.IamInstanceProfile).To(BeNil()) },
				),
			)
		})

		When("configuring tags", func() {
			It("should include default and Name tags correctly with baseline config", func() {
				runInput, _ := ecConfig.configureInstance(taskRunName, instanceTag, additionalTags)
				Expect(runInput.TagSpecifications).To(HaveLen(1))
				Expect(runInput.TagSpecifications[0].ResourceType).To(Equal(types.ResourceTypeInstance))
				tags := runInput.TagSpecifications[0].Tags
				Expect(tags).To(ContainElement(SatisfyAll(HaveField("Key", PointTo(Equal(MultiPlatformManaged))), HaveField("Value", PointTo(Equal("true"))))))
				Expect(tags).To(ContainElement(SatisfyAll(HaveField("Key", PointTo(Equal(cloud.InstanceTag))), HaveField("Value", Equal(&instanceTag)))))
				Expect(tags).To(ContainElement(SatisfyAll(HaveField("Key", PointTo(Equal("Name"))), HaveField("Value", PointTo(Equal("multi-platform-builder-"+taskRunName))))))
			})

			It("should include default and additional tags when provided", func() {
				additionalTags["CostCenter"] = "AlphaTeam"
				runInput, _ := ecConfig.configureInstance(taskRunName, instanceTag, additionalTags)
				tags := runInput.TagSpecifications[0].Tags
				Expect(tags).To(HaveLen(3 + len(additionalTags)))
				Expect(tags).To(ContainElement(SatisfyAll(HaveField("Key", PointTo(Equal("CostCenter"))), HaveField("Value", PointTo(Equal("AlphaTeam"))))))
			})
		})

		When("configuring EBS block device mappings", func() {
			It("should set EBS properties correctly based on baseline config", func() {
				runInput, _ := ecConfig.configureInstance(taskRunName, instanceTag, additionalTags)
				Expect(runInput.BlockDeviceMappings).To(HaveLen(1))
				bdMapping := runInput.BlockDeviceMappings[0]
				Expect(bdMapping.DeviceName).To(PointTo(Equal("/dev/sda1")))
				Expect(bdMapping.Ebs).NotTo(BeNil())
				Expect(bdMapping.Ebs.DeleteOnTermination).To(PointTo(BeTrue()))
				Expect(bdMapping.Ebs.VolumeSize).To(Equal(&ecConfig.Disk))
				Expect(bdMapping.Ebs.VolumeType).To(Equal(types.VolumeTypeGp3))
			})

			DescribeTable("with various IOPS and Throughput settings",
				func(iops *int32, throughput *int32) {
					ecConfig.Iops = iops
					ecConfig.Throughput = throughput
					runInput, _ := ecConfig.configureInstance(taskRunName, instanceTag, additionalTags)
					ebsDevice := runInput.BlockDeviceMappings[0].Ebs
					Expect(ebsDevice.Iops).To(Equal(iops))
					Expect(ebsDevice.Throughput).To(Equal(throughput))
				},
				Entry("using baseline IOPS and Throughput", newDefaultValidEC2ConfigForInstance().Iops, newDefaultValidEC2ConfigForInstance().Throughput),
				Entry("with custom IOPS and Throughput", aws.Int32(5000), aws.Int32(300)),
				Entry("with only IOPS set (clearing baseline Throughput)", aws.Int32(5500), nil),
				Entry("with only Throughput set (clearing baseline Iops)", nil, aws.Int32(350)),
				Entry("with neither IOPS nor Throughput set (clearing baseline for both)", nil, nil),
			)
		})

		When("UserData is provided (already base64 encoded)", func() {
			It("should pass it through to RunInstancesInput using encoded commonUserData", func() {
				// commonUserData is from aws_test.go (raw script)
				encodedCommonUserData := base64.StdEncoding.EncodeToString([]byte(commonUserData))
				ecConfig.UserData = &encodedCommonUserData
				runInput, _ := ecConfig.configureInstance(taskRunName, instanceTag, additionalTags)
				Expect(runInput.UserData).To(Equal(ecConfig.UserData))
			})

			It("should be nil if ecConfig.UserData is set to nil (overriding baseline if it had UserData)", func() {
				ecConfig.UserData = nil
				runInput, _ := ecConfig.configureInstance(taskRunName, instanceTag, additionalTags)
				Expect(runInput.UserData).To(Equal(ecConfig.UserData))
			})
		})

		When("configuring MacOS dedicated host instances", func() {
			It("should successfully configure when all three MacOS fields are set", func() {
				ecConfig.Tenancy = "host"
				ecConfig.HostResourceGroupArn = "arn:aws:resource-groups:us-east-1:123456789012:group/mac-hosts"
				ecConfig.LicenseConfigurationArn = "arn:aws:license-manager:us-east-1:123456789012:license-configuration:lic-macos"
				runInput, err := ecConfig.configureInstance(taskRunName, instanceTag, additionalTags)

				Expect(err).ShouldNot(HaveOccurred())
				Expect(runInput.Placement).ShouldNot(BeNil())
				Expect(runInput.Placement.Tenancy).Should(Equal(types.TenancyHost))
				Expect(runInput.Placement.HostResourceGroupArn).Should(PointTo(Equal("arn:aws:resource-groups:us-east-1:123456789012:group/mac-hosts")))
				Expect(runInput.LicenseSpecifications).Should(HaveLen(1))
				Expect(runInput.LicenseSpecifications[0].LicenseConfigurationArn).Should(PointTo(Equal("arn:aws:license-manager:us-east-1:123456789012:license-configuration:lic-macos")))
			})

			DescribeTable("should return validation error when only one MacOS field is set",
				func(setupConfig func() AWSEc2DynamicConfig) {
					currentConfig := setupConfig()
					_, err := currentConfig.configureInstance(taskRunName, instanceTag, additionalTags)
					Expect(err).Should(HaveOccurred())
					Expect(err.Error()).Should(ContainSubstring("MacOS"))
				},
				Entry("with only Tenancy set",
					func() AWSEc2DynamicConfig {
						conf := newDefaultValidEC2ConfigForInstance()
						conf.Tenancy = "host"
						return conf
					},
				),
				Entry("with only HostResourceGroupArn set",
					func() AWSEc2DynamicConfig {
						conf := newDefaultValidEC2ConfigForInstance()
						conf.HostResourceGroupArn = "arn:aws:resource-groups:us-east-1:123456789012:group/mac-host-group"
						return conf
					},
				),
				Entry("with only LicenseConfigurationArn set",
					func() AWSEc2DynamicConfig {
						conf := newDefaultValidEC2ConfigForInstance()
						conf.LicenseConfigurationArn = "arn:aws:license-manager:us-east-1:123456789012:license-configuration:lic-abc123"
						return conf
					},
				),
			)
		})
	})

	Describe("parseInstanceTypes", func() {
		DescribeTable("parsing the raw value",
			func(raw string, expected []string) {
				instanceTypes, err := parseInstanceTypes(raw)

				Expect(err).ShouldNot(HaveOccurred())
				Expect(instanceTypes).Should(Equal(expected))
			},
			Entry("empty value", "", nil),
			Entry("single entry", "c5.xlarge", []string{"c5.xlarge"}),
			Entry("multiple entries", "c5.xlarge,c6i.xlarge,m6a.xlarge", []string{"c5.xlarge", "c6i.xlarge", "m6a.xlarge"}),
			Entry("entries are trimmed", " c5.xlarge , c6i.xlarge ", []string{"c5.xlarge", "c6i.xlarge"}),
			Entry("trailing comma is tolerated", "c5.xlarge,", []string{"c5.xlarge"}),
			Entry("arm64 types of several generations mix fine", "t4g.medium,m6gd.large,x8g.48xlarge", []string{"t4g.medium", "m6gd.large", "x8g.48xlarge"}),
			Entry("unrecognized families default to x86_64", "c9v.xlarge,c5.xlarge", []string{"c9v.xlarge", "c5.xlarge"}),
		)

		DescribeTable("rejecting malformed values",
			func(raw string, expectedMessage string) {
				_, err := parseInstanceTypes(raw)

				Expect(err).Should(MatchError(ContainSubstring(expectedMessage)))
			},
			Entry("empty entry in the middle", "c5.xlarge,,c6i.xlarge", "empty instance type"),
			Entry("leading comma", ",c5.xlarge", "empty instance type"),
			Entry("comma only", ",", "empty instance type"),
			Entry("space entry in the middle", "c5.xlarge, ,c6i.xlarge", "empty instance type"),
			Entry("architecture mix", "c5.xlarge,t4g.micro", "mixing architectures"),
			Entry("architecture mix with arm64 first", "t4g.micro,c5.xlarge", "mixing architectures"),
		)
	})

	Describe("instanceTypeArchitecture", func() {
		DescribeTable("architecture by family",
			func(instanceType string, expected string) {
				Expect(instanceTypeArchitecture(instanceType)).Should(Equal(expected))
			},
			Entry("x86_64 compute family", "c5.xlarge", "x86_64"),
			Entry("x86_64 general family", "m5.large", "x86_64"),
			Entry("x86_64 memory family", "r7i.8xlarge", "x86_64"),
			Entry("x86_64 burstable family", "t3.micro", "x86_64"),
			Entry("x86_64 inferentia-adjacent naming", "inf1.24xlarge", "x86_64"),
			Entry("arm64 burstable family", "t4g.medium", "arm64"),
			Entry("arm64 graviton compute family", "c6g.large", "arm64"),
			Entry("arm64 graviton compute with nvme variant", "c6gd.xlarge", "arm64"),
			Entry("arm64 graviton compute with network variant", "c6gn.xlarge", "arm64"),
			Entry("arm64 graviton general family", "m6g.large", "arm64"),
			Entry("arm64 graviton memory family", "r8g.48xlarge", "arm64"),
			Entry("arm64 first generation", "a1.large", "arm64"),
			Entry("arm64 gpu family", "g5g.xlarge", "arm64"),
			Entry("arm64 high-memory family", "x2gd.2xlarge", "arm64"),
			Entry("arm64 newest high-memory family", "x8g.48xlarge", "arm64"),
			Entry("arm64 intell family with suffix", "is4gen.2xlarge", "arm64"),
			Entry("arm64 intell family with nvme suffix", "im4gn.16xlarge", "arm64"),
		)
	})

	Describe("configureFleet", func() {
		var ecConfig AWSEc2DynamicConfig

		BeforeEach(func() {
			ecConfig = newDefaultValidEC2ConfigForInstance()
			ecConfig.InstanceTypes = []string{"c5.xlarge", "c6i.xlarge", "m6a.xlarge"}
		})

		It("should build an instant one-instance on-demand fleet", func() {
			input := ecConfig.configureFleet("mpc-linux-amd64", "my-task", "my-tag", map[string]string{})

			Expect(input.Type).Should(Equal(types.FleetTypeInstant))
			Expect(input.TargetCapacitySpecification).ShouldNot(BeNil())
			Expect(*input.TargetCapacitySpecification.TotalTargetCapacity).Should(Equal(int32(1)))
			Expect(input.TargetCapacitySpecification.DefaultTargetCapacityType).Should(Equal(types.DefaultTargetCapacityTypeOnDemand))
			Expect(input.OnDemandOptions).ShouldNot(BeNil())
			Expect(input.OnDemandOptions.AllocationStrategy).Should(Equal(types.FleetOnDemandAllocationStrategyPrioritized))
			Expect(input.SpotOptions).Should(BeNil())
		})

		It("should reference the launch template as $Default with one override per type in the configured subnet", func() {
			input := ecConfig.configureFleet("mpc-linux-amd64", "my-task", "my-tag", map[string]string{})

			Expect(input.LaunchTemplateConfigs).Should(HaveLen(1))
			templateConfig := input.LaunchTemplateConfigs[0]
			Expect(*templateConfig.LaunchTemplateSpecification.LaunchTemplateName).Should(Equal("mpc-linux-amd64"))
			Expect(*templateConfig.LaunchTemplateSpecification.Version).Should(Equal("$Default"))
			Expect(templateConfig.Overrides).Should(HaveLen(3))
			for i, override := range templateConfig.Overrides {
				Expect(override.SubnetId).Should(PointTo(Equal(ecConfig.SubnetId)))
				Expect(override.Priority).Should(PointTo(Equal(float64(i))))
			}
			Expect(string(templateConfig.Overrides[0].InstanceType)).Should(Equal("c5.xlarge"))
			Expect(string(templateConfig.Overrides[1].InstanceType)).Should(Equal("c6i.xlarge"))
			Expect(string(templateConfig.Overrides[2].InstanceType)).Should(Equal("m6a.xlarge"))
		})

		It("should tag the instance and the volume with the managed-instance tags", func() {
			input := ecConfig.configureFleet("mpc-linux-amd64", "my-task", "my-tag", map[string]string{"CostCenter": "AlphaTeam"})

			Expect(input.TagSpecifications).Should(HaveLen(2))
			Expect(input.TagSpecifications[0].ResourceType).Should(Equal(types.ResourceTypeInstance))
			Expect(input.TagSpecifications[1].ResourceType).Should(Equal(types.ResourceTypeVolume))
			tags := input.TagSpecifications[0].Tags
			Expect(tags).Should(HaveLen(4))
			Expect(tags).Should(ContainElement(SatisfyAll(
				HaveField("Key", PointTo(Equal(MultiPlatformManaged))),
				HaveField("Value", PointTo(Equal("true"))))))
			Expect(tags).Should(ContainElement(SatisfyAll(
				HaveField("Key", PointTo(Equal(cloud.InstanceTag))),
				HaveField("Value", PointTo(Equal("my-tag"))))))
			Expect(tags).Should(ContainElement(SatisfyAll(
				HaveField("Key", PointTo(Equal("Name"))),
				HaveField("Value", PointTo(Equal("multi-platform-builder-my-task"))))))
			Expect(tags).Should(ContainElement(SatisfyAll(
				HaveField("Key", PointTo(Equal("CostCenter"))),
				HaveField("Value", PointTo(Equal("AlphaTeam"))))))
		})
	})

	Describe("ensureLaunchTemplate", func() {
		var (
			mock     *mockEC2Client
			ecConfig AWSEc2DynamicConfig
		)

		BeforeEach(func() {
			resetLaunchTemplateCache()
			mock = &mockEC2Client{}
			ecConfig = newDefaultValidEC2ConfigForInstance()
			ecConfig.platformName = "linux-amd64"
		})

		When("the launch template does not exist", func() {
			It("should create it with the platform configuration", func(ctx SpecContext) {
				mock.DescribeLaunchTemplatesOutput = &ec2.DescribeLaunchTemplatesOutput{}

				name, err := ecConfig.ensureLaunchTemplate(mock, ctx)

				Expect(err).ShouldNot(HaveOccurred())
				Expect(name).Should(Equal("mpc-linux-amd64"))
				Expect(mock.CreateLaunchTemplateInput).ShouldNot(BeNil())
				Expect(*mock.CreateLaunchTemplateInput.LaunchTemplateName).Should(Equal("mpc-linux-amd64"))

				data := mock.CreateLaunchTemplateInput.LaunchTemplateData
				Expect(data).ShouldNot(BeNil())
				Expect(*data.ImageId).Should(Equal(ecConfig.Ami))
				Expect(*data.KeyName).Should(Equal(ecConfig.KeyName))
				Expect(data.SecurityGroupIds).Should(ConsistOf(ecConfig.SecurityGroupId))
				Expect(data.SecurityGroups).Should(BeNil())
				Expect(data.EbsOptimized).Should(PointTo(BeTrue()))
				Expect(data.UserData).Should(Equal(ecConfig.UserData))
				Expect(data.IamInstanceProfile).ShouldNot(BeNil())
				Expect(*data.IamInstanceProfile.Arn).Should(Equal(ecConfig.InstanceProfileArn))
				Expect(data.InstanceInitiatedShutdownBehavior).Should(Equal(types.ShutdownBehaviorTerminate))

				Expect(data.BlockDeviceMappings).Should(HaveLen(1))
				mapping := data.BlockDeviceMappings[0]
				Expect(*mapping.DeviceName).Should(Equal("/dev/sda1"))
				Expect(mapping.Ebs).ShouldNot(BeNil())
				Expect(mapping.Ebs.DeleteOnTermination).Should(PointTo(BeTrue()))
				Expect(mapping.Ebs.VolumeSize).Should(PointTo(Equal(ecConfig.Disk)))
				Expect(mapping.Ebs.VolumeType).Should(Equal(types.VolumeTypeGp3))
				Expect(mapping.Ebs.Iops).Should(Equal(ecConfig.Iops))
				Expect(mapping.Ebs.Throughput).Should(Equal(ecConfig.Throughput))

				Expect(mock.CreateLaunchTemplateVersionInput).Should(BeNil())
				Expect(mock.ModifyLaunchTemplateInput).Should(BeNil())
			})

			It("should treat a NotFound describe error as missing", func(ctx SpecContext) {
				mock.DescribeLaunchTemplatesErr = errors.New("InvalidLaunchTemplateName.NotFoundException: The specified launch template was not found")

				name, err := ecConfig.ensureLaunchTemplate(mock, ctx)

				Expect(err).ShouldNot(HaveOccurred())
				Expect(name).Should(Equal("mpc-linux-amd64"))
				Expect(mock.CreateLaunchTemplateInput).ShouldNot(BeNil())
			})
		})

		When("the launch template exists and is already cached for this configuration", func() {
			It("should not create a new version", func(ctx SpecContext) {
				mock.DescribeLaunchTemplatesOutput = &ec2.DescribeLaunchTemplatesOutput{
					LaunchTemplates: []types.LaunchTemplate{{LaunchTemplateName: aws.String("mpc-linux-amd64")}},
				}
				mock.CreateLaunchTemplateVersionOutput = &ec2.CreateLaunchTemplateVersionOutput{
					LaunchTemplateVersion: &types.LaunchTemplateVersion{VersionNumber: aws.Int64(3)},
				}

				_, err := ecConfig.ensureLaunchTemplate(mock, ctx)
				Expect(err).ShouldNot(HaveOccurred())
				Expect(mock.CreateLaunchTemplateVersionInput).ShouldNot(BeNil())
				mock.CreateLaunchTemplateVersionInput = nil
				mock.ModifyLaunchTemplateInput = nil

				_, err = ecConfig.ensureLaunchTemplate(mock, ctx)
				Expect(err).ShouldNot(HaveOccurred())
				Expect(mock.CreateLaunchTemplateVersionInput).Should(BeNil())
				Expect(mock.CreateLaunchTemplateInput).Should(BeNil())
				Expect(mock.ModifyLaunchTemplateInput).Should(BeNil())
			})
		})

		When("the launch template exists with a different configuration", func() {
			It("should create a version and set it as the default", func(ctx SpecContext) {
				mock.DescribeLaunchTemplatesOutput = &ec2.DescribeLaunchTemplatesOutput{
					LaunchTemplates: []types.LaunchTemplate{{LaunchTemplateName: aws.String("mpc-linux-amd64")}},
				}
				mock.CreateLaunchTemplateVersionOutput = &ec2.CreateLaunchTemplateVersionOutput{
					LaunchTemplateVersion: &types.LaunchTemplateVersion{VersionNumber: aws.Int64(7)},
				}

				_, err := ecConfig.ensureLaunchTemplate(mock, ctx)

				Expect(err).ShouldNot(HaveOccurred())
				Expect(mock.CreateLaunchTemplateInput).Should(BeNil())
				Expect(mock.CreateLaunchTemplateVersionInput).ShouldNot(BeNil())
				Expect(*mock.CreateLaunchTemplateVersionInput.LaunchTemplateName).Should(Equal("mpc-linux-amd64"))
				Expect(mock.CreateLaunchTemplateVersionInput.LaunchTemplateData).ShouldNot(BeNil())
				Expect(mock.ModifyLaunchTemplateInput).ShouldNot(BeNil())
				Expect(*mock.ModifyLaunchTemplateInput.LaunchTemplateName).Should(Equal("mpc-linux-amd64"))
				Expect(*mock.ModifyLaunchTemplateInput.DefaultVersion).Should(Equal("7"))
			})

			It("should create a new version when the configuration hash changes", func(ctx SpecContext) {
				mock.DescribeLaunchTemplatesOutput = &ec2.DescribeLaunchTemplatesOutput{
					LaunchTemplates: []types.LaunchTemplate{{LaunchTemplateName: aws.String("mpc-linux-amd64")}},
				}
				mock.CreateLaunchTemplateVersionOutput = &ec2.CreateLaunchTemplateVersionOutput{
					LaunchTemplateVersion: &types.LaunchTemplateVersion{VersionNumber: aws.Int64(4)},
				}

				_, err := ecConfig.ensureLaunchTemplate(mock, ctx)
				Expect(err).ShouldNot(HaveOccurred())
				mock.CreateLaunchTemplateVersionInput = nil

				ecConfig.Ami = "ami-changed"
				_, err = ecConfig.ensureLaunchTemplate(mock, ctx)
				Expect(err).ShouldNot(HaveOccurred())
				Expect(mock.CreateLaunchTemplateVersionInput).ShouldNot(BeNil())
				Expect(*mock.CreateLaunchTemplateVersionInput.LaunchTemplateData.ImageId).Should(Equal("ami-changed"))
			})

			It("should fail when the version response carries no version number", func(ctx SpecContext) {
				mock.DescribeLaunchTemplatesOutput = &ec2.DescribeLaunchTemplatesOutput{
					LaunchTemplates: []types.LaunchTemplate{{LaunchTemplateName: aws.String("mpc-linux-amd64")}},
				}
				mock.CreateLaunchTemplateVersionOutput = &ec2.CreateLaunchTemplateVersionOutput{}

				_, err := ecConfig.ensureLaunchTemplate(mock, ctx)

				Expect(err).Should(MatchError(ContainSubstring("returned no version number")))
				Expect(mock.ModifyLaunchTemplateInput).Should(BeNil())
			})
		})

		When("the platform name is not configured", func() {
			It("should return an error", func(ctx SpecContext) {
				ecConfig.platformName = ""

				_, err := ecConfig.ensureLaunchTemplate(mock, ctx)

				Expect(err).Should(MatchError(ContainSubstring("platform name is required")))
			})
		})

		When("an equivalent configuration is rebuilt with fresh pointers", func() {
			It("should hash identically and not create a new version", func(ctx SpecContext) {
				mock.DescribeLaunchTemplatesOutput = &ec2.DescribeLaunchTemplatesOutput{
					LaunchTemplates: []types.LaunchTemplate{{LaunchTemplateName: aws.String("mpc-linux-amd64")}},
				}
				mock.CreateLaunchTemplateVersionOutput = &ec2.CreateLaunchTemplateVersionOutput{
					LaunchTemplateVersion: &types.LaunchTemplateVersion{VersionNumber: aws.Int64(2)},
				}

				_, err := ecConfig.ensureLaunchTemplate(mock, ctx)
				Expect(err).ShouldNot(HaveOccurred())
				mock.CreateLaunchTemplateVersionInput = nil

				rebuilt := newDefaultValidEC2ConfigForInstance()
				rebuilt.platformName = "linux-amd64"
				rebuilt.Iops = aws.Int32(*ecConfig.Iops)
				rebuilt.Throughput = aws.Int32(*ecConfig.Throughput)
				_, err = rebuilt.ensureLaunchTemplate(mock, ctx)
				Expect(err).ShouldNot(HaveOccurred())
				Expect(mock.CreateLaunchTemplateVersionInput).Should(BeNil())
			})
		})

		When("describing the launch template fails", func() {
			It("should return the error", func(ctx SpecContext) {
				mock.DescribeLaunchTemplatesErr = errors.New("AccessDenied")

				_, err := ecConfig.ensureLaunchTemplate(mock, ctx)

				Expect(err).Should(MatchError(ContainSubstring("failed to describe launch template")))
			})
		})

		When("creating the launch template fails", func() {
			It("should return the error", func(ctx SpecContext) {
				mock.DescribeLaunchTemplatesOutput = &ec2.DescribeLaunchTemplatesOutput{}
				mock.CreateLaunchTemplateErr = errors.New("template quota exceeded")

				_, err := ecConfig.ensureLaunchTemplate(mock, ctx)

				Expect(err).Should(MatchError(ContainSubstring("failed to create launch template")))
			})
		})

		When("creating a launch template version fails", func() {
			It("should return the error", func(ctx SpecContext) {
				mock.DescribeLaunchTemplatesOutput = &ec2.DescribeLaunchTemplatesOutput{
					LaunchTemplates: []types.LaunchTemplate{{LaunchTemplateName: aws.String("mpc-linux-amd64")}},
				}
				mock.CreateLaunchTemplateVersionErr = errors.New("version rejected")

				_, err := ecConfig.ensureLaunchTemplate(mock, ctx)

				Expect(err).Should(MatchError(ContainSubstring("failed to create launch template version")))
			})
		})

		When("setting the default version fails", func() {
			It("should return the error", func(ctx SpecContext) {
				mock.DescribeLaunchTemplatesOutput = &ec2.DescribeLaunchTemplatesOutput{
					LaunchTemplates: []types.LaunchTemplate{{LaunchTemplateName: aws.String("mpc-linux-amd64")}},
				}
				mock.CreateLaunchTemplateVersionOutput = &ec2.CreateLaunchTemplateVersionOutput{
					LaunchTemplateVersion: &types.LaunchTemplateVersion{VersionNumber: aws.Int64(8)},
				}
				mock.ModifyLaunchTemplateErr = errors.New("modify rejected")

				_, err := ecConfig.ensureLaunchTemplate(mock, ctx)

				Expect(err).Should(MatchError(ContainSubstring("failed to set default launch template version")))
			})
		})
	})

	Describe("launchTemplateData", func() {
		It("should mirror the security group name when only a name is configured", func() {
			ecConfig := newDefaultValidEC2ConfigForInstance()
			ecConfig.SecurityGroupId = ""
			ecConfig.SecurityGroup = "sg-name-example"

			data := ecConfig.launchTemplateData()

			Expect(data.SecurityGroups).Should(ConsistOf("sg-name-example"))
			Expect(data.SecurityGroupIds).Should(BeNil())
		})

		It("should omit the IAM instance profile when none is configured", func() {
			ecConfig := newDefaultValidEC2ConfigForInstance()
			ecConfig.InstanceProfileArn = ""
			ecConfig.InstanceProfileName = ""

			data := ecConfig.launchTemplateData()

			Expect(data.IamInstanceProfile).Should(BeNil())
		})

		It("should carry the instance profile name when only a name is configured", func() {
			ecConfig := newDefaultValidEC2ConfigForInstance()
			ecConfig.InstanceProfileArn = ""
			ecConfig.InstanceProfileName = "profile-name-example"

			data := ecConfig.launchTemplateData()

			Expect(data.IamInstanceProfile).ShouldNot(BeNil())
			Expect(*data.IamInstanceProfile.Name).Should(Equal("profile-name-example"))
			Expect(data.IamInstanceProfile.Arn).Should(BeNil())
		})
	})

	Describe("SecretCredentialsProvider Retrieve", func() {
		var s *runtime.Scheme

		BeforeEach(func() {
			s = runtime.NewScheme()
			Expect(corev1.AddToScheme(s)).Should(Succeed())
		})

		When("kubeClient is nil", func() {
			It("should return credentials from environment variables without error", func(ctx SpecContext) {
				provider := SecretCredentialsProvider{Client: nil}

				_, err := provider.Retrieve(ctx)

				Expect(err).ShouldNot(HaveOccurred())
			})
		})

		When("kubeClient is provided", func() {
			It("should return credentials from the Kubernetes secret", func(ctx SpecContext) {
				secret := &corev1.Secret{
					ObjectMeta: metav1.ObjectMeta{Name: "aws-creds", Namespace: "ns"},
					Data: map[string][]byte{
						"access-key-id":     []byte("AKID"),
						"secret-access-key": []byte("SECRET"),
						"session-token":     []byte("TOKEN"),
					},
				}
				fakeClient := fake.NewClientBuilder().WithScheme(s).WithObjects(secret).Build()
				provider := SecretCredentialsProvider{Name: "aws-creds", Namespace: "ns", Client: fakeClient}

				creds, err := provider.Retrieve(ctx)

				Expect(err).ShouldNot(HaveOccurred())
				Expect(creds.AccessKeyID).Should(Equal("AKID"))
				Expect(creds.SecretAccessKey).Should(Equal("SECRET"))
				Expect(creds.SessionToken).Should(Equal("TOKEN"))
			})

			It("should return credentials without session token when it is absent", func(ctx SpecContext) {
				secret := &corev1.Secret{
					ObjectMeta: metav1.ObjectMeta{Name: "aws-creds", Namespace: "ns"},
					Data: map[string][]byte{
						"access-key-id":     []byte("AKID"),
						"secret-access-key": []byte("SECRET"),
					},
				}
				fakeClient := fake.NewClientBuilder().WithScheme(s).WithObjects(secret).Build()
				provider := SecretCredentialsProvider{Name: "aws-creds", Namespace: "ns", Client: fakeClient}

				creds, err := provider.Retrieve(ctx)

				Expect(err).ShouldNot(HaveOccurred())
				Expect(creds.SessionToken).Should(BeEmpty())
			})

			It("should return error when the secret does not exist", func(ctx SpecContext) {
				fakeClient := fake.NewClientBuilder().WithScheme(s).Build()
				provider := SecretCredentialsProvider{Name: "missing", Namespace: "ns", Client: fakeClient}

				_, err := provider.Retrieve(ctx)

				Expect(err).Should(HaveOccurred())
				Expect(err.Error()).Should(ContainSubstring("failed to retrieve the secret"))
			})
		})
	})

})

// Helper function to create a baseline valid AWSEc2DynamicConfig for configureInstance tests
func newDefaultValidEC2ConfigForInstance() AWSEc2DynamicConfig {
	var defaultUserDataPtr *string
	// For the baseline, let's assume UserData is provided via commonUserData and thus needs encoding
	if commonUserData != "" { // commonUserData is the raw script
		encoded := base64.StdEncoding.EncodeToString([]byte(commonUserData))
		defaultUserDataPtr = &encoded
	}

	return AWSEc2DynamicConfig{
		Region:             "us-west-1", // Neutral baseline values
		Ami:                "ami-default123",
		InstanceType:       "t2.medium",
		KeyName:            "default-key",
		Secret:             "default-secret",
		SystemNamespace:    "default-sys-namespace",
		SecurityGroupId:    "sg-default00000000000",
		SubnetId:           "subnet-default00000000",
		Disk:               int32(40),
		InstanceProfileArn: "arn:aws:iam::000000000000:instance-profile/default-instance-profile",
		Iops:               aws.Int32(3000),
		Throughput:         aws.Int32(125),
		UserData:           defaultUserDataPtr,
	}
}

// resetLaunchTemplateCache clears the in-process launch template version cache
// between tests so each spec exercises the describe/create/version flow itself.
func resetLaunchTemplateCache() {
	launchTemplateCache.Lock()
	launchTemplateCache.hashes = map[string]string{}
	launchTemplateCache.Unlock()
}
