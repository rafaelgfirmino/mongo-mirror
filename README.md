# mongo-mirror
Simple tool to mirror data from one MongoDB to another MongoDB
```yaml
config:
  source:
    connectionString: mongodb://user:password@localhost:27017
    database: DBNameSource
  destiny:
    connectionString: mongodb://ebpay:1q2w3E*@localhost:27017
    database: DBNameDestiny
  tenants:
    - "3a0dbbaa-35b4-c4fd-d0e4-0d08ef38bea3" #multiTenant use TenantId property
    - "7051fc14-0ad0-4fb1-c679-39ff7ec38024"
collections:
  - name: "Invoices" # Collection you want to mirror
    batchSize: "all" # Number of documents to be imported
    upsert: true # If you want to update the document if it already exists
    multiTenant: false # If you want to use the TenantId property to filter the documents, if false it will mirror all documents ignoring the TenantId property in config
    filter: | # Filter to be used in the query, maybe you want to mirror only the documents that have a specific status
      {
        "TenantId": "3a0dbbaa-35b4-c4fd-d0e4-0d08ef38bea3",
        "Status": "Paid"
      }
```