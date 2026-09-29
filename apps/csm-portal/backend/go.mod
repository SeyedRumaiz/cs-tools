module github.com/wso2-open-operations/cs-tools/apps/csm-portal/backend

go 1.26.6

require (
	github.com/MicahParks/keyfunc/v3 v3.8.0
	github.com/golang-jwt/jwt/v5 v5.3.1
	github.com/wso2-open-operations/cs-tools/apps/chat-routing-service/sdk-go v0.0.0-00010101000000-000000000000
	github.com/wso2-open-operations/cs-tools/apps/live-chat-sdk/sdk-go v0.0.0-00010101000000-000000000000
	golang.org/x/oauth2 v0.27.0
)

require (
	github.com/MicahParks/jwkset v0.11.0 // indirect
	golang.org/x/time v0.9.0 // indirect
)

replace github.com/wso2-open-operations/cs-tools/apps/chat-routing-service/sdk-go => ../../chat-routing-service/sdk-go

replace github.com/wso2-open-operations/cs-tools/apps/live-chat-sdk/sdk-go => ../../live-chat-sdk/sdk-go
