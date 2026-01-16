package dynamodb

import (
	"log/slog"
	"reflect"
	"sort"
	"sync"

	"aws-in-a-box/arn"
	"aws-in-a-box/awserrors"
)

type Table struct {
	Name                 string
	ARN                  string
	BillingMode          string
	AttributeDefinitions []APIAttributeDefinition
	attributeDefinitions map[string]APIAttributeType
	KeySchema            []APIKeySchemaElement

	PrimaryKeyAttributeName string
	PrimaryKeyAttributeType string
	ItemByPrimaryKey        map[string]APIItem
}

func (t *Table) toAPI() APITableDescription {
	return APITableDescription{
		AttributeDefinitions: t.AttributeDefinitions,
		ItemCount:            len(t.ItemByPrimaryKey),
		KeySchema:            t.KeySchema,
		// TODO: delayed creation
		TableARN:    t.ARN,
		TableStatus: "ACTIVE",
	}
}

type DynamoDB struct {
	logger       *slog.Logger
	arnGenerator arn.Generator

	mu           sync.Mutex
	tablesByName map[string]*Table
}

type Options struct {
	Logger       *slog.Logger
	ArnGenerator arn.Generator
}

func New(options Options) *DynamoDB {
	if options.Logger == nil {
		options.Logger = slog.Default()
	}

	d := &DynamoDB{
		logger:       options.Logger,
		arnGenerator: options.ArnGenerator,
		tablesByName: make(map[string]*Table),
	}
	return d
}

// https://docs.aws.amazon.com/amazondynamodb/latest/APIReference/API_CreateTable.html
func (d *DynamoDB) CreateTable(input CreateTableInput) (*CreateTableOutput, *awserrors.Error) {
	d.mu.Lock()
	defer d.mu.Unlock()

	if _, ok := d.tablesByName[input.TableName]; ok {
		return nil, awserrors.ResourceInUseException("Table already exists")
	}

	primaryKeyAttributeName := ""
	for _, keySchemaElement := range input.KeySchema {
		if keySchemaElement.KeyType == "HASH" {
			primaryKeyAttributeName = keySchemaElement.AttributeName
			break
		}
	}
	if primaryKeyAttributeName == "" {
		return nil, awserrors.InvalidArgumentException("KeySchema must have a HASH key")
	}

	attributeDefinitions := make(map[string]APIAttributeType)
	for _, def := range input.AttributeDefinitions {
		attributeDefinitions[def.AttributeName] = def.AttributeType
	}

	t := &Table{
		Name:                    input.TableName,
		ARN:                     d.arnGenerator.Generate("dynamodb", "table", input.TableName),
		BillingMode:             input.BillingMode,
		AttributeDefinitions:    input.AttributeDefinitions,
		attributeDefinitions:    attributeDefinitions,
		KeySchema:               input.KeySchema,
		PrimaryKeyAttributeName: primaryKeyAttributeName,
		ItemByPrimaryKey:        make(map[string]APIItem),
	}
	d.tablesByName[input.TableName] = t

	return &CreateTableOutput{
		TableDescription: t.toAPI(),
	}, nil
}

// https://docs.aws.amazon.com/amazondynamodb/latest/APIReference/API_CreateTable.html
func (d *DynamoDB) DescribeTable(input DescribeTableInput) (*DescribeTableOutput, *awserrors.Error) {
	d.mu.Lock()
	defer d.mu.Unlock()

	t, ok := d.tablesByName[input.TableName]
	if !ok {
		return nil, awserrors.ResourceNotFoundException("Table does not exist")
	}

	return &DescribeTableOutput{
		Table: t.toAPI(),
	}, nil
}

// https://docs.aws.amazon.com/amazondynamodb/latest/APIReference/API_Scan.html
func (d *DynamoDB) Scan(input ScanInput) (*ScanOutput, *awserrors.Error) {
	d.mu.Lock()
	defer d.mu.Unlock()

	t, ok := d.tablesByName[input.TableName]
	if !ok {
		return nil, awserrors.ResourceNotFoundException("Table does not exist")
	}

	var allItems []APIItem
	for _, item := range t.ItemByPrimaryKey {
		// TODO: filter
		allItems = append(allItems, item)
	}

	return &ScanOutput{
		Count: len(allItems),
		Items: allItems,
	}, nil
}

func (d *DynamoDB) lockedGetPrimaryKeyFromItem(table *Table, item map[string]APIAttributeValue) string {
	// TODO: composite keys
	primaryKeyAttribute := item[table.PrimaryKeyAttributeName]

	primaryKeyType := table.attributeDefinitions[table.PrimaryKeyAttributeName]
	switch primaryKeyType {
	case AttributeType_String:
		return primaryKeyAttribute.S
	case AttributeType_Binary:
		return primaryKeyAttribute.B
	case AttributeType_Numeric:
		return primaryKeyAttribute.N
	default:
		panic("Unreachable")
	}
}

// https://docs.aws.amazon.com/amazondynamodb/latest/APIReference/API_PutItem.html
func (d *DynamoDB) PutItem(input PutItemInput) (*PutItemOutput, *awserrors.Error) {
	d.mu.Lock()
	defer d.mu.Unlock()

	t, ok := d.tablesByName[input.TableName]
	if !ok {
		return nil, awserrors.ResourceNotFoundException("Table does not exist")
	}

	// TODO: composite keys
	key := d.lockedGetPrimaryKeyFromItem(t, input.Item)
	if key == "" {
		return nil, awserrors.InvalidArgumentException("PrimaryKey must be provided (and string)")
	}

	output := &PutItemOutput{}
	if input.ReturnValues == PutItems_ALL_OLD {
		output.Attributes = t.ItemByPrimaryKey[key]
	}

	t.ItemByPrimaryKey[key] = input.Item

	return output, nil
}

// https://docs.aws.amazon.com/amazondynamodb/latest/APIReference/API_GetItem.html
func (d *DynamoDB) GetItem(input GetItemInput) (*GetItemOutput, *awserrors.Error) {
	d.mu.Lock()
	defer d.mu.Unlock()

	t, ok := d.tablesByName[input.TableName]
	if !ok {
		return nil, awserrors.ResourceNotFoundException("Table does not exist")
	}

	// TODO: composite keys
	key := d.lockedGetPrimaryKeyFromItem(t, input.Key)
	if key == "" {
		return nil, awserrors.InvalidArgumentException("PrimaryKey must be provided")
	}
	item := t.ItemByPrimaryKey[key]

	return &GetItemOutput{Item: item}, nil
}

// https://docs.aws.amazon.com/amazondynamodb/latest/APIReference/API_UpdateItem.html
func (d *DynamoDB) UpdateItem(input UpdateItemInput) (*UpdateItemOutput, *awserrors.Error) {
	d.mu.Lock()
	defer d.mu.Unlock()

	t, ok := d.tablesByName[input.TableName]
	if !ok {
		return nil, awserrors.ResourceNotFoundException("Table does not exist")
	}

	// TODO: composite keys
	key := d.lockedGetPrimaryKeyFromItem(t, input.Key)
	if key == "" {
		return nil, awserrors.InvalidArgumentException("PrimaryKey must be provided")
	}
	existingItem, ok := t.ItemByPrimaryKey[key]

	if !ok {
		existingItem = make(map[string]APIAttributeValue)
	}

	// Check preconditions
	for attribute, expectation := range input.Expected {
		attr, exists := existingItem[attribute]
		if expectation.Exists != nil {
			if *expectation.Exists != exists {
				return nil, awserrors.ConditionalCheckFailedException("Attribute exists mismatch")
			}
		}
		switch expectation.ComparisonOperator {
		case "":
		case "EQ":
			if !reflect.DeepEqual(attr, expectation.Value) {
				return nil, awserrors.ConditionalCheckFailedException("Attribute EQ mismatch")
			}
		case "NEQ":
			if reflect.DeepEqual(attr, expectation.Value) {
				return nil, awserrors.ConditionalCheckFailedException("Attribute NEQ mismatch")
			}
		default:
			return nil, awserrors.InvalidArgumentException("Invalid expectation comparison operator: " + expectation.ComparisonOperator)
		}
	}

	// Perform update
	// TODO: handle ReturnValues
	for attribute, update := range input.AttributeUpdates {
		switch update.Action {
		case "PUT":
			existingItem[attribute] = update.Value
		case "DELETE":
			delete(existingItem, attribute)
		case "ADD":
			// TODO
			// fallthrough
		default:
			return nil, awserrors.InvalidArgumentException("Invalid update action: " + update.Action)
		}
	}

	// If this was an insert, not an update, we need to commit it.
	if !ok {
		t.ItemByPrimaryKey[key] = existingItem
	}

	return &UpdateItemOutput{}, nil
}

// https://docs.aws.amazon.com/amazondynamodb/latest/APIReference/API_DeleteItem.html
func (d *DynamoDB) DeleteItem(input DeleteItemInput) (*DeleteItemOutput, *awserrors.Error) {
	d.mu.Lock()
	defer d.mu.Unlock()

	t, ok := d.tablesByName[input.TableName]
	if !ok {
		return nil, awserrors.ResourceNotFoundException("Table does not exist")
	}

	key := d.lockedGetPrimaryKeyFromItem(t, input.Key)
	if key == "" {
		return nil, awserrors.InvalidArgumentException("PrimaryKey must be provided")
	}

	existingItem, exists := t.ItemByPrimaryKey[key]

	// Check preconditions (Expected parameter)
	for attribute, expectation := range input.Expected {
		attr, attrExists := existingItem[attribute]
		if expectation.Exists != nil {
			if *expectation.Exists != attrExists {
				return nil, awserrors.ConditionalCheckFailedException("Attribute exists mismatch")
			}
		}
		switch expectation.ComparisonOperator {
		case "":
		case "EQ":
			if !reflect.DeepEqual(attr, expectation.Value) {
				return nil, awserrors.ConditionalCheckFailedException("Attribute EQ mismatch")
			}
		case "NE":
			if reflect.DeepEqual(attr, expectation.Value) {
				return nil, awserrors.ConditionalCheckFailedException("Attribute NE mismatch")
			}
		default:
			return nil, awserrors.InvalidArgumentException("Invalid expectation comparison operator: " + expectation.ComparisonOperator)
		}
	}

	output := &DeleteItemOutput{}
	if input.ReturnValues == DeleteItem_ALL_OLD && exists {
		output.Attributes = existingItem
	}

	delete(t.ItemByPrimaryKey, key)

	return output, nil
}

// https://docs.aws.amazon.com/amazondynamodb/latest/APIReference/API_DeleteTable.html
func (d *DynamoDB) DeleteTable(input DeleteTableInput) (*DeleteTableOutput, *awserrors.Error) {
	d.mu.Lock()
	defer d.mu.Unlock()

	t, ok := d.tablesByName[input.TableName]
	if !ok {
		return nil, awserrors.ResourceNotFoundException("Table does not exist")
	}

	desc := t.toAPI()
	desc.TableStatus = "DELETING"

	delete(d.tablesByName, input.TableName)

	return &DeleteTableOutput{
		TableDescription: desc,
	}, nil
}

// https://docs.aws.amazon.com/amazondynamodb/latest/APIReference/API_ListTables.html
func (d *DynamoDB) ListTables(input ListTablesInput) (*ListTablesOutput, *awserrors.Error) {
	d.mu.Lock()
	defer d.mu.Unlock()

	var names []string
	for name := range d.tablesByName {
		names = append(names, name)
	}

	// Sort for consistent ordering
	sort.Strings(names)

	// Handle pagination
	startIdx := 0
	if input.ExclusiveStartTableName != "" {
		for i, name := range names {
			if name == input.ExclusiveStartTableName {
				startIdx = i + 1
				break
			}
		}
	}

	if startIdx >= len(names) {
		return &ListTablesOutput{
			TableNames: []string{},
		}, nil
	}

	names = names[startIdx:]

	limit := input.Limit
	if limit <= 0 {
		limit = 100 // Default limit per AWS docs
	}

	var lastEvaluatedTableName string
	if len(names) > limit {
		lastEvaluatedTableName = names[limit-1]
		names = names[:limit]
	}

	return &ListTablesOutput{
		TableNames:             names,
		LastEvaluatedTableName: lastEvaluatedTableName,
	}, nil
}

// https://docs.aws.amazon.com/amazondynamodb/latest/APIReference/API_Query.html
func (d *DynamoDB) Query(input QueryInput) (*QueryOutput, *awserrors.Error) {
	d.mu.Lock()
	defer d.mu.Unlock()

	t, ok := d.tablesByName[input.TableName]
	if !ok {
		return nil, awserrors.ResourceNotFoundException("Table does not exist")
	}

	// For now, support only KeyConditions (legacy API)
	// A full implementation would parse KeyConditionExpression
	if len(input.KeyConditions) == 0 && input.KeyConditionExpression == "" {
		return nil, awserrors.InvalidArgumentException("KeyConditions or KeyConditionExpression required")
	}

	var results []APIItem

	// Check KeyConditions for partition key match
	for _, item := range t.ItemByPrimaryKey {
		matches := true

		if len(input.KeyConditions) > 0 {
			for attrName, condition := range input.KeyConditions {
				itemValue, exists := item[attrName]
				if !exists {
					matches = false
					break
				}

				if len(condition.AttributeValueList) == 0 {
					matches = false
					break
				}

				switch condition.ComparisonOperator {
				case "EQ":
					if !reflect.DeepEqual(itemValue, condition.AttributeValueList[0]) {
						matches = false
					}
				default:
					// For simplicity, only support EQ for now
					matches = false
				}

				if !matches {
					break
				}
			}
		}

		if matches {
			results = append(results, item)
		}
	}

	// Apply limit
	var lastEvaluatedKey map[string]APIAttributeValue
	if input.Limit > 0 && len(results) > input.Limit {
		lastItem := results[input.Limit-1]
		lastEvaluatedKey = map[string]APIAttributeValue{
			t.PrimaryKeyAttributeName: lastItem[t.PrimaryKeyAttributeName],
		}
		results = results[:input.Limit]
	}

	return &QueryOutput{
		Count:            len(results),
		Items:            results,
		LastEvaluatedKey: lastEvaluatedKey,
		ScannedCount:     len(results),
	}, nil
}

// https://docs.aws.amazon.com/amazondynamodb/latest/APIReference/API_BatchGetItem.html
func (d *DynamoDB) BatchGetItem(input BatchGetItemInput) (*BatchGetItemOutput, *awserrors.Error) {
	d.mu.Lock()
	defer d.mu.Unlock()

	responses := make(map[string][]APIItem)
	unprocessedKeys := make(map[string]KeysAndAttributes)

	for tableName, keysAndAttributes := range input.RequestItems {
		t, ok := d.tablesByName[tableName]
		if !ok {
			return nil, awserrors.ResourceNotFoundException("Table does not exist: " + tableName)
		}

		var items []APIItem
		for _, keyMap := range keysAndAttributes.Keys {
			key := d.lockedGetPrimaryKeyFromItem(t, keyMap)
			if key == "" {
				continue
			}
			if item, exists := t.ItemByPrimaryKey[key]; exists {
				items = append(items, item)
			}
		}
		responses[tableName] = items
	}

	return &BatchGetItemOutput{
		Responses:       responses,
		UnprocessedKeys: unprocessedKeys,
	}, nil
}

// https://docs.aws.amazon.com/amazondynamodb/latest/APIReference/API_BatchWriteItem.html
func (d *DynamoDB) BatchWriteItem(input BatchWriteItemInput) (*BatchWriteItemOutput, *awserrors.Error) {
	d.mu.Lock()
	defer d.mu.Unlock()

	unprocessedItems := make(map[string][]WriteRequest)

	for tableName, writeRequests := range input.RequestItems {
		t, ok := d.tablesByName[tableName]
		if !ok {
			return nil, awserrors.ResourceNotFoundException("Table does not exist: " + tableName)
		}

		for _, req := range writeRequests {
			if req.PutRequest != nil {
				key := d.lockedGetPrimaryKeyFromItem(t, req.PutRequest.Item)
				if key == "" {
					continue
				}
				t.ItemByPrimaryKey[key] = req.PutRequest.Item
			}
			if req.DeleteRequest != nil {
				key := d.lockedGetPrimaryKeyFromItem(t, req.DeleteRequest.Key)
				if key == "" {
					continue
				}
				delete(t.ItemByPrimaryKey, key)
			}
		}
	}

	return &BatchWriteItemOutput{
		UnprocessedItems: unprocessedItems,
	}, nil
}
