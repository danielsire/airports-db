FUNCTION_NAME=airports-db

build:
	GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -tags lambda.norpc -o bootstrap ./cmd/lambda
	zip -j ${FUNCTION_NAME}.zip bootstrap
update-lambda:
	AWS_ACCESS_KEY_ID="test" \
	AWS_SECRET_ACCESS_KEY="test" \
	AWS_DEFAULT_REGION="us-east-1" \
	aws --endpoint-url http://localhost:4566 lambda update-function-code \
	--function-name ${FUNCTION_NAME} \
    --zip-file fileb://${FUNCTION_NAME}.zip
