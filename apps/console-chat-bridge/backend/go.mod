module github.com/wso2-open-operations/cs-tools/apps/console-chat-bridge/backend

go 1.26.6

require (
	github.com/wso2-open-operations/cs-tools/apps/live-chat-sdk/sdk-go v0.0.0-00010101000000-000000000000
	golang.org/x/oauth2 v0.27.0
)

replace github.com/wso2-open-operations/cs-tools/apps/live-chat-sdk/sdk-go => ../../live-chat-sdk/sdk-go
