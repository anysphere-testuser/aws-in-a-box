package itest

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"

	"aws-in-a-box/arn"
	"aws-in-a-box/server"
	dynamodbImpl "aws-in-a-box/services/dynamodb"
)

const region = "us-east-2"

func makeClientServerPair() (*dynamodb.Client, *http.Server) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		panic(err)
	}
	impl := dynamodbImpl.New(dynamodbImpl.Options{
		ArnGenerator: arn.Generator{
			AwsAccountId: "666354587717",
			Region:       region,
		},
	})

	methodRegistry := make(map[string]http.HandlerFunc)
	impl.RegisterHTTPHandlers(slog.Default(), methodRegistry)

	srv := server.NewWithHandlerChain(
		server.HandlerFuncFromRegistry(slog.Default(), methodRegistry),
	)
	go srv.Serve(listener)

	client := dynamodb.New(dynamodb.Options{
		EndpointResolver: dynamodb.EndpointResolverFromURL("http://" + listener.Addr().String()),
		Retryer:          aws.NopRetryer{},
	})

	return client, srv
}

func TestGetItem_PartitionKey(t *testing.T) {
	ctx := context.Background()
	client, srv := makeClientServerPair()
	defer srv.Shutdown(ctx)

	primaryKey := aws.String("pkey")

	for _, primaryKeyType := range types.ScalarAttributeType("").Values() {
		t.Run("_"+string(primaryKeyType), func(t *testing.T) {
			tableName := "table_" + string(primaryKeyType)
			_, err := client.CreateTable(ctx, &dynamodb.CreateTableInput{
				AttributeDefinitions: []types.AttributeDefinition{
					{
						AttributeName: primaryKey,
						AttributeType: primaryKeyType,
					},
				},
				KeySchema: []types.KeySchemaElement{
					{
						AttributeName: primaryKey,
						KeyType:       types.KeyTypeHash,
					},
				},
				TableName: &tableName,
			})
			if err != nil {
				t.Fatal(err)
			}

			var primaryKeyValue types.AttributeValue
			switch primaryKeyType {
			case types.ScalarAttributeTypeS:
				primaryKeyValue = &types.AttributeValueMemberS{Value: "key"}
			case types.ScalarAttributeTypeN:
				primaryKeyValue = &types.AttributeValueMemberN{Value: "key"}
			case types.ScalarAttributeTypeB:
				primaryKeyValue = &types.AttributeValueMemberB{Value: []byte("key")}
			default:
				t.Fatal("Unknown type")
			}

			_, err = client.PutItem(ctx, &dynamodb.PutItemInput{
				TableName: &tableName,
				Item:      map[string]types.AttributeValue{*primaryKey: primaryKeyValue},
			})
			if err != nil {
				t.Fatal(err)
			}

			_, err = client.GetItem(ctx, &dynamodb.GetItemInput{
				TableName: &tableName,
				Key:       map[string]types.AttributeValue{*primaryKey: primaryKeyValue},
			})
			if err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestScanItem_FilterExpression_StringPrimaryKey(t *testing.T) {
	ctx := context.Background()
	client, srv := makeClientServerPair()
	defer srv.Shutdown(ctx)

	primaryKey := "pkey"

	tableName := "table"
	_, err := client.CreateTable(ctx, &dynamodb.CreateTableInput{
		AttributeDefinitions: []types.AttributeDefinition{
			{
				AttributeName: aws.String(primaryKey),
				AttributeType: types.ScalarAttributeTypeS,
			},
		},
		KeySchema: []types.KeySchemaElement{
			{
				AttributeName: aws.String(primaryKey),
				KeyType:       types.KeyTypeHash,
			},
		},
		TableName: &tableName,
	})
	if err != nil {
		t.Fatal(err)
	}

	// Create 1, 11, 2, 22, 3, 33
	for i := 1; i <= 3; i++ {
		v := strconv.Itoa(i)
		_, err = client.PutItem(ctx, &dynamodb.PutItemInput{
			TableName: &tableName,
			Item: map[string]types.AttributeValue{
				primaryKey: &types.AttributeValueMemberS{Value: v},
			},
		})
		if err != nil {
			t.Fatal(err)
		}
		_, err = client.PutItem(ctx, &dynamodb.PutItemInput{
			TableName: &tableName,
			Item: map[string]types.AttributeValue{
				primaryKey: &types.AttributeValueMemberS{Value: v + v},
			},
		})
		if err != nil {
			t.Fatal(err)
		}
	}

	resp, err := client.Scan(ctx, &dynamodb.ScanInput{
		TableName: &tableName,
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Count != 6 {
		t.Fatal("missing items")
	}

	resp, err = client.Scan(ctx, &dynamodb.ScanInput{
		TableName:        &tableName,
		FilterExpression: aws.String(primaryKey + " >= \"2\""),
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Count != 4 {
		fmt.Println("TODO!")
		//t.Fatal("filter not working: ", resp.Count)
	}
}

func TestDeleteItem(t *testing.T) {
	ctx := context.Background()
	client, srv := makeClientServerPair()
	defer srv.Shutdown(ctx)

	primaryKey := "pkey"
	tableName := "test_delete_item"

	// Create table
	_, err := client.CreateTable(ctx, &dynamodb.CreateTableInput{
		AttributeDefinitions: []types.AttributeDefinition{
			{
				AttributeName: aws.String(primaryKey),
				AttributeType: types.ScalarAttributeTypeS,
			},
		},
		KeySchema: []types.KeySchemaElement{
			{
				AttributeName: aws.String(primaryKey),
				KeyType:       types.KeyTypeHash,
			},
		},
		TableName: &tableName,
	})
	if err != nil {
		t.Fatal(err)
	}

	// Put an item
	_, err = client.PutItem(ctx, &dynamodb.PutItemInput{
		TableName: &tableName,
		Item: map[string]types.AttributeValue{
			primaryKey: &types.AttributeValueMemberS{Value: "key1"},
			"data":     &types.AttributeValueMemberS{Value: "value1"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	// Verify item exists
	getResp, err := client.GetItem(ctx, &dynamodb.GetItemInput{
		TableName: &tableName,
		Key: map[string]types.AttributeValue{
			primaryKey: &types.AttributeValueMemberS{Value: "key1"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(getResp.Item) == 0 {
		t.Fatal("Item should exist before delete")
	}

	// Delete item and request old values
	delResp, err := client.DeleteItem(ctx, &dynamodb.DeleteItemInput{
		TableName: &tableName,
		Key: map[string]types.AttributeValue{
			primaryKey: &types.AttributeValueMemberS{Value: "key1"},
		},
		ReturnValues: types.ReturnValueAllOld,
	})
	if err != nil {
		t.Fatal(err)
	}

	// Check returned attributes
	if len(delResp.Attributes) == 0 {
		t.Fatal("Expected attributes from deleted item")
	}

	// Verify item is gone
	getResp, err = client.GetItem(ctx, &dynamodb.GetItemInput{
		TableName: &tableName,
		Key: map[string]types.AttributeValue{
			primaryKey: &types.AttributeValueMemberS{Value: "key1"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(getResp.Item) != 0 {
		t.Fatal("Item should not exist after delete")
	}
}

func TestListTables(t *testing.T) {
	ctx := context.Background()
	client, srv := makeClientServerPair()
	defer srv.Shutdown(ctx)

	primaryKey := "pkey"

	// Create multiple tables
	tableNames := []string{"alpha", "beta", "gamma"}
	for _, name := range tableNames {
		_, err := client.CreateTable(ctx, &dynamodb.CreateTableInput{
			AttributeDefinitions: []types.AttributeDefinition{
				{
					AttributeName: aws.String(primaryKey),
					AttributeType: types.ScalarAttributeTypeS,
				},
			},
			KeySchema: []types.KeySchemaElement{
				{
					AttributeName: aws.String(primaryKey),
					KeyType:       types.KeyTypeHash,
				},
			},
			TableName: aws.String(name),
		})
		if err != nil {
			t.Fatal(err)
		}
	}

	// List tables
	listResp, err := client.ListTables(ctx, &dynamodb.ListTablesInput{})
	if err != nil {
		t.Fatal(err)
	}

	if len(listResp.TableNames) != 3 {
		t.Fatalf("Expected 3 tables, got %d", len(listResp.TableNames))
	}

	// Tables should be sorted
	expected := []string{"alpha", "beta", "gamma"}
	for i, name := range listResp.TableNames {
		if name != expected[i] {
			t.Fatalf("Expected table %s at position %d, got %s", expected[i], i, name)
		}
	}
}

func TestDeleteTable(t *testing.T) {
	ctx := context.Background()
	client, srv := makeClientServerPair()
	defer srv.Shutdown(ctx)

	primaryKey := "pkey"
	tableName := "test_delete_table"

	// Create table
	_, err := client.CreateTable(ctx, &dynamodb.CreateTableInput{
		AttributeDefinitions: []types.AttributeDefinition{
			{
				AttributeName: aws.String(primaryKey),
				AttributeType: types.ScalarAttributeTypeS,
			},
		},
		KeySchema: []types.KeySchemaElement{
			{
				AttributeName: aws.String(primaryKey),
				KeyType:       types.KeyTypeHash,
			},
		},
		TableName: &tableName,
	})
	if err != nil {
		t.Fatal(err)
	}

	// Verify table exists
	_, err = client.DescribeTable(ctx, &dynamodb.DescribeTableInput{
		TableName: &tableName,
	})
	if err != nil {
		t.Fatal(err)
	}

	// Delete table
	delResp, err := client.DeleteTable(ctx, &dynamodb.DeleteTableInput{
		TableName: &tableName,
	})
	if err != nil {
		t.Fatal(err)
	}

	// Verify the response contains table description
	if delResp.TableDescription == nil {
		t.Fatal("Expected TableDescription in response")
	}
	if delResp.TableDescription.TableStatus != types.TableStatusDeleting {
		t.Fatalf("Expected status DELETING, got %s", delResp.TableDescription.TableStatus)
	}

	// Verify table is gone
	_, err = client.DescribeTable(ctx, &dynamodb.DescribeTableInput{
		TableName: &tableName,
	})
	if err == nil {
		t.Fatal("Expected error for deleted table")
	}
}

func TestQuery(t *testing.T) {
	ctx := context.Background()
	client, srv := makeClientServerPair()
	defer srv.Shutdown(ctx)

	primaryKey := "pkey"
	tableName := "test_query"

	// Create table
	_, err := client.CreateTable(ctx, &dynamodb.CreateTableInput{
		AttributeDefinitions: []types.AttributeDefinition{
			{
				AttributeName: aws.String(primaryKey),
				AttributeType: types.ScalarAttributeTypeS,
			},
		},
		KeySchema: []types.KeySchemaElement{
			{
				AttributeName: aws.String(primaryKey),
				KeyType:       types.KeyTypeHash,
			},
		},
		TableName: &tableName,
	})
	if err != nil {
		t.Fatal(err)
	}

	// Put some items
	for i := 1; i <= 3; i++ {
		_, err = client.PutItem(ctx, &dynamodb.PutItemInput{
			TableName: &tableName,
			Item: map[string]types.AttributeValue{
				primaryKey: &types.AttributeValueMemberS{Value: fmt.Sprintf("key%d", i)},
				"data":     &types.AttributeValueMemberS{Value: fmt.Sprintf("value%d", i)},
			},
		})
		if err != nil {
			t.Fatal(err)
		}
	}

	// Query for specific key using KeyConditions (legacy)
	queryResp, err := client.Query(ctx, &dynamodb.QueryInput{
		TableName: &tableName,
		KeyConditions: map[string]types.Condition{
			primaryKey: {
				AttributeValueList: []types.AttributeValue{
					&types.AttributeValueMemberS{Value: "key2"},
				},
				ComparisonOperator: types.ComparisonOperatorEq,
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	if queryResp.Count != 1 {
		t.Fatalf("Expected 1 item, got %d", queryResp.Count)
	}
}
