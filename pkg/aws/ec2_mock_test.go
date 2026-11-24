package aws

import (
	"context"

	"github.com/aws/aws-sdk-go-v2/service/ec2"
)

// mockEC2Client satisfies the ec2API interface for testing.
// Output/Err fields control return values; Input fields capture the last call.
type mockEC2Client struct {
	RunInstancesOutput      *ec2.RunInstancesOutput
	RunInstancesErr         error
	RunInstancesInput       *ec2.RunInstancesInput
	DescribeInstancesOutput *ec2.DescribeInstancesOutput
	DescribeInstancesErr    error
	DescribeInstancesInput  *ec2.DescribeInstancesInput
	TerminateInstancesErr   error

	CreateFleetOutput *ec2.CreateFleetOutput
	CreateFleetErr    error
	CreateFleetInput  *ec2.CreateFleetInput

	CreateLaunchTemplateOutput *ec2.CreateLaunchTemplateOutput
	CreateLaunchTemplateErr    error
	CreateLaunchTemplateInput  *ec2.CreateLaunchTemplateInput

	DescribeLaunchTemplatesOutput *ec2.DescribeLaunchTemplatesOutput
	DescribeLaunchTemplatesErr    error
	DescribeLaunchTemplatesInput  *ec2.DescribeLaunchTemplatesInput

	CreateLaunchTemplateVersionOutput *ec2.CreateLaunchTemplateVersionOutput
	CreateLaunchTemplateVersionErr    error
	CreateLaunchTemplateVersionInput  *ec2.CreateLaunchTemplateVersionInput

	ModifyLaunchTemplateOutput *ec2.ModifyLaunchTemplateOutput
	ModifyLaunchTemplateErr    error
	ModifyLaunchTemplateInput  *ec2.ModifyLaunchTemplateInput
}

func (m *mockEC2Client) RunInstances(_ context.Context, input *ec2.RunInstancesInput, _ ...func(*ec2.Options)) (*ec2.RunInstancesOutput, error) {
	m.RunInstancesInput = input
	return m.RunInstancesOutput, m.RunInstancesErr
}

func (m *mockEC2Client) DescribeInstances(_ context.Context, input *ec2.DescribeInstancesInput, _ ...func(*ec2.Options)) (*ec2.DescribeInstancesOutput, error) {
	m.DescribeInstancesInput = input
	return m.DescribeInstancesOutput, m.DescribeInstancesErr
}

func (m *mockEC2Client) TerminateInstances(_ context.Context, _ *ec2.TerminateInstancesInput, _ ...func(*ec2.Options)) (*ec2.TerminateInstancesOutput, error) {
	return nil, m.TerminateInstancesErr
}

func (m *mockEC2Client) CreateFleet(_ context.Context, input *ec2.CreateFleetInput, _ ...func(*ec2.Options)) (*ec2.CreateFleetOutput, error) {
	m.CreateFleetInput = input
	return m.CreateFleetOutput, m.CreateFleetErr
}

func (m *mockEC2Client) CreateLaunchTemplate(_ context.Context, input *ec2.CreateLaunchTemplateInput, _ ...func(*ec2.Options)) (*ec2.CreateLaunchTemplateOutput, error) {
	m.CreateLaunchTemplateInput = input
	return m.CreateLaunchTemplateOutput, m.CreateLaunchTemplateErr
}

func (m *mockEC2Client) DescribeLaunchTemplates(_ context.Context, input *ec2.DescribeLaunchTemplatesInput, _ ...func(*ec2.Options)) (*ec2.DescribeLaunchTemplatesOutput, error) {
	m.DescribeLaunchTemplatesInput = input
	return m.DescribeLaunchTemplatesOutput, m.DescribeLaunchTemplatesErr
}

func (m *mockEC2Client) CreateLaunchTemplateVersion(_ context.Context, input *ec2.CreateLaunchTemplateVersionInput, _ ...func(*ec2.Options)) (*ec2.CreateLaunchTemplateVersionOutput, error) {
	m.CreateLaunchTemplateVersionInput = input
	return m.CreateLaunchTemplateVersionOutput, m.CreateLaunchTemplateVersionErr
}

func (m *mockEC2Client) ModifyLaunchTemplate(_ context.Context, input *ec2.ModifyLaunchTemplateInput, _ ...func(*ec2.Options)) (*ec2.ModifyLaunchTemplateOutput, error) {
	m.ModifyLaunchTemplateInput = input
	return m.ModifyLaunchTemplateOutput, m.ModifyLaunchTemplateErr
}
