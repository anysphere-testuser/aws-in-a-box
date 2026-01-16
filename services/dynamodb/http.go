package dynamodb

import (
	"log/slog"

	"aws-in-a-box/http"
)

const service = "DynamoDB_20120810"

func (d *DynamoDB) RegisterHTTPHandlers(logger *slog.Logger, methodRegistry http.Registry) {
	http.Register(logger, methodRegistry, service, "BatchGetItem", d.BatchGetItem)
	http.Register(logger, methodRegistry, service, "BatchWriteItem", d.BatchWriteItem)
	http.Register(logger, methodRegistry, service, "CreateTable", d.CreateTable)
	http.Register(logger, methodRegistry, service, "DeleteItem", d.DeleteItem)
	http.Register(logger, methodRegistry, service, "DeleteTable", d.DeleteTable)
	http.Register(logger, methodRegistry, service, "DescribeTable", d.DescribeTable)
	http.Register(logger, methodRegistry, service, "GetItem", d.GetItem)
	http.Register(logger, methodRegistry, service, "ListTables", d.ListTables)
	http.Register(logger, methodRegistry, service, "PutItem", d.PutItem)
	http.Register(logger, methodRegistry, service, "Query", d.Query)
	http.Register(logger, methodRegistry, service, "Scan", d.Scan)
	http.Register(logger, methodRegistry, service, "UpdateItem", d.UpdateItem)
}
