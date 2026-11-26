LAMBDA_NAME=airports-db
LOCALSTACK_URL=http://localstack:4566
ROLE_NAME="lambda-role"
REGION=us-east-1
STAGE=dev

awslocal iam create-role \
  --role-name $ROLE_NAME \
  --assume-role-policy-document '{"Version": "2012-10-17","Statement": [{ "Effect": "Allow", "Principal": {"Service": "lambda.amazonaws.com"}, "Action": "sts:AssumeRole"}]}'

awslocal lambda create-function \
  --function-name $LAMBDA_NAME \
  --runtime provided.al2023 \
  --handler bootstrap \
  --zip-file fileb://$LAMBDA_NAME.zip \
  --role arn:aws:iam::000000000000:role/$ROLE_NAME \
  --region $REGION \
  --timeout 30 \
  --memory-size 512
  # --- API Gateway (REST) ---
  API_ID=$(awslocal apigateway create-rest-api \
    --name "$LAMBDA_NAME-api" \
    --region $REGION \
    --query id --output text)

  ROOT_ID=$(awslocal apigateway get-resources \
    --rest-api-id $API_ID --region $REGION \
    --query 'items[0].id' --output text)

  RESOURCE_ID=$(awslocal apigateway create-resource \
    --rest-api-id $API_ID --region $REGION \
    --parent-id $ROOT_ID --path-part loader \
    --query id --output text)

  awslocal apigateway put-method \
    --rest-api-id $API_ID --region $REGION \
    --resource-id $RESOURCE_ID --http-method ANY \
    --authorization-type NONE

  awslocal apigateway put-integration \
    --rest-api-id $API_ID --region $REGION \
    --resource-id $RESOURCE_ID --http-method ANY \
    --type AWS_PROXY --integration-http-method POST \
    --uri arn:aws:apigateway:$REGION:lambda:path/2015-03-31/functions/arn:aws:lambda:$REGION:000000000000:function:$LAMBDA_NAME/invocations

  awslocal lambda add-permission \
    --function-name $LAMBDA_NAME --region $REGION \
    --statement-id apigw-invoke \
    --action lambda:InvokeFunction \
    --principal apigateway.amazonaws.com \
    --source-arn "arn:aws:execute-api:$REGION:000000000000:$API_ID/*/*/loader"

  awslocal apigateway create-deployment \
    --rest-api-id $API_ID --region $REGION --stage-name $STAGE

  echo "API_ID=$API_ID"
  echo "http://localhost:4566/restapis/$API_ID/$STAGE/_user_request_/loader"
