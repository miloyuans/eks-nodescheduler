// central/go.mod
module central

go 1.25

require (
	github.com/aws/aws-sdk-go-v2 v1.30.4 // 最新稳定版，2025年底
	github.com/aws/aws-sdk-go-v2/config v1.27.29
	github.com/aws/aws-sdk-go-v2/credentials v1.17.29
	github.com/aws/aws-sdk-go-v2/service/autoscaling v1.43.3
	github.com/aws/aws-sdk-go-v2/service/eks v1.48.3
	github.com/go-telegram-bot-api/telegram-bot-api/v5 v5.5.1 // 长期未更新，但仍可用
	go.mongodb.org/mongo-driver v2.4.1 // v2 最新系列
	golang.org/x/net v0.40.0
	google.golang.org/grpc v1.78.0 // 最新
	google.golang.org/protobuf v1.36.6
	gopkg.in/yaml.v3 v3.0.1
)

require (
	// indirect dependencies 会由 go mod tidy 自动添加
	github.com/aws/aws-sdk-go-v2/feature/ec2/imds v1.16.12 // indirect
	github.com/aws/aws-sdk-go-v2/internal/configsources v1.3.16 // indirect
	github.com/aws/aws-sdk-go-v2/internal/endpoints/v2 v2.6.16 // indirect
	github.com/aws/aws-sdk-go-v2/internal/ini v1.8.1 // indirect
	github.com/aws/aws-sdk-go-v2/service/internal/accept-encoding v1.11.4 // indirect
	github.com/aws/aws-sdk-go-v2/service/internal/presigned-url v1.11.18 // indirect
	github.com/aws/aws-sdk-go-v2/service/sso v1.22.5 // indirect
	github.com/aws/aws-sdk-go-v2/service/ssooidc v1.26.5 // indirect
	github.com/aws/aws-sdk-go-v2/service/sts v1.30.6 // indirect
	github.com/aws/smithy-go v1.20.4 // indirect
	golang.org/x/sys v0.33.0 // indirect
	golang.org/x/text v0.25.0 // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20250519155744-55703ea1f237 // indirect
)