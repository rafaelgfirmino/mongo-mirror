package mongoSync

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

type Direction string

const (
	Source  Direction = "Source Database"
	Destiny Direction = "Destiny Database"
)

type MongoConfig struct {
	ConnectionString string `yaml:"connectionString"`
	Database         string `yaml:"database"`
	Connection       *mongo.Client
}
type Config struct {
	SourceClient  MongoConfig `yaml:"source"`
	DestinyClient MongoConfig `yaml:"destiny"`
	Tenants       []string    `yaml:"tenants"`
	TenantDesity  string      `yaml:"tenantDestiny"`
	Timeout       int         `yaml:"timeout"`
}
type Collection struct {
	Name        string `yaml:"name"`
	BatchSize   string `yaml:"batchSize"`
	MultiTenant string `yaml:"multiTenant"`
	Filter      string `yaml:"filter"`
	Upsert      string `yaml:"upsert"`
}
type Mirror struct {
	Configs     Config       `yaml:"config"`
	Collections []Collection `yaml:"collections"`
}

func (m *Mirror) LoadConfig() {
	ctx := context.Context(context.Background())
	wg := sync.WaitGroup{}
	wg.Add(2)
	var err error
	go func(wg *sync.WaitGroup) {
		defer wg.Done()
		m.Configs.SourceClient.Connection, err = connectDb(ctx, m.Configs.SourceClient.ConnectionString, Source)
		if err != nil {
			log.Fatal(err)
		}
	}(&wg)

	go func(wg *sync.WaitGroup) {
		defer wg.Done()
		m.Configs.DestinyClient.Connection, err = connectDb(ctx, m.Configs.DestinyClient.ConnectionString, Destiny)
		if err != nil {
			log.Fatal(err)
		}
	}(&wg)
	wg.Wait()
}
func connectDb(ctx context.Context, connectionString string, direction Direction) (*mongo.Client, error) {
	fmt.Println(direction, connectionString)
	if direction == Destiny && strings.Contains(connectionString, "mongodb.net") {
		panic("You can't connect to a mongodb atlas, the destiny database can't be a production database.")
	}
	log.Printf("Start connect mongodb %s", direction)
	source, err := mongo.Connect(ctx, options.Client().ApplyURI(connectionString))
	if err != nil {
		log.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	if err := source.Ping(ctx, nil); err != nil {
		return nil, fmt.Errorf("failed to ping MongoDB -> %s: \n %s", direction, err)
	}

	log.Printf("Successfully connected to MongoDB! %s \n", direction)
	return source, nil
}

func (m *Mirror) LoadCollections() {
	if m.Configs.Timeout == 0 {
		m.Configs.Timeout = 60
	}
	ctxWithTimeout, cancel := context.WithTimeout(context.Background(), time.Duration(m.Configs.Timeout)*time.Second)
	defer cancel()

	for _, collection := range m.Collections {
		fmt.Printf("--------------------------Starting collection %s-------------------------- \n", collection.Name)
		dbDestiny(ctxWithTimeout, m.Configs, collection)
		fmt.Printf("--------------------------Finished import collection %s-------------------------- \n\n", collection.Name)
	}
}

func dbSource(ctx context.Context, db Config, collection Collection) *mongo.Cursor {
	sourceCollection := db.SourceClient.Connection.Database(db.SourceClient.Database).Collection(collection.Name)
	var filter bson.M
	if collection.MultiTenant == "" {
		collection.MultiTenant = "true"
	}
	if collection.MultiTenant == "true" && len(db.Tenants) > 0 {
		filterTenant := []byte(fmt.Sprintf(`{"TenantId": {"$in": ["%s"]}}`, strings.Join(db.Tenants, `","`)))
		if err := json.Unmarshal(filterTenant, &filter); err != nil {
			log.Fatalf("Error unmarshalling json1: %v", err)
		}
	}

	if collection.Filter != "" {
		if err := json.Unmarshal([]byte(collection.Filter), &filter); err != nil {
			log.Fatal(err)
		}
	}

	if err := convertUUIDs(filter); err != nil {
		log.Fatalf("Failed to convert UUIDs in filter for collection %s: %v", collection.Name, err)
	}

	findOptions := options.Find() // Ajuste o tamanho do lote conforme necessário
	countOptions := options.Count()

	if collection.BatchSize == "" {
		collection.BatchSize = "all"
	}

	if collection.BatchSize != "all" {
		i, err := strconv.Atoi(collection.BatchSize)
		if err != nil {
			log.Fatal("BatchSize must be a number or 'all'")
			panic(err)
		}
		findOptions.SetLimit(int64(i))
		countOptions.SetLimit(int64(i))
	}
	count, err := sourceCollection.CountDocuments(ctx, filter, countOptions)
	if err != nil {
		log.Fatal(err)
	}

	fmt.Printf("Collection %s has %d documents to be imported\n", collection.Name, count)
	cursor, err := sourceCollection.Find(ctx, filter, findOptions)

	if err != nil {
		log.Fatal(err)
	}
	return cursor
}
func dbDestiny(ctx context.Context, db Config, collection Collection) {
	destinyCollection := db.DestinyClient.Connection.Database(db.DestinyClient.Database).Collection(collection.Name)
	cursor := dbSource(ctx, db, collection)
	defer cursor.Close(ctx)

	var documents []interface{}
	var copiedCount int64
	tenantDestiny, _ := uuidToBinary(db.TenantDesity)
	if collection.Upsert == "true" || collection.Upsert == "" {
		for cursor.Next(ctx) {
			var document bson.M
			if err := cursor.Decode(&document); err != nil {
				log.Fatal(err)
			}
			//if document["TenantId"] == nil && !tenantDestiny.IsZero() {
			document["TenantId"] = tenantDestiny
			//}
			_, err := destinyCollection.UpdateOne(
				ctx,
				bson.M{"_id": document["_id"]}, // Filtro baseado no ID do documento
				bson.M{"$set": document},
				options.Update().SetUpsert(true),
			)
			if err != nil {
				log.Fatal(err)
			}

			copiedCount += 1
		}
	} else {
		if err := cursor.All(ctx, &documents); err != nil {
			log.Fatal(err)
		}

		if len(documents) > 0 {
			_, err := destinyCollection.InsertMany(ctx, documents)
			if err != nil {
				log.Fatal(err)
			}
			copiedCount += int64(len(documents))
		}
	}

	fmt.Printf("Collection %s imported successfully! Total: %d \n", collection.Name, copiedCount)
}

func uuidToBinary(u string) (primitive.Binary, error) {
	id, err := uuid.Parse(u)
	if err != nil {
		return primitive.Binary{}, fmt.Errorf("failed to parse UUID: %v", err)
	}
	return primitive.Binary{
		Subtype: 4,
		Data:    id[:],
	}, nil
}

func convertUUIDs(filter bson.M) error {
	for key, value := range filter {
		switch v := value.(type) {
		case string:
			if strings.Contains(v, "-") {
				if binary, err := uuidToBinary(v); err == nil {
					filter[key] = binary
				} else {
					return err
				}
			}
		case []interface{}:
			for i, item := range v {
				if str, ok := item.(string); ok && strings.Contains(str, "-") {
					if binary, err := uuidToBinary(str); err == nil {
						v[i] = binary
					} else {
						return err
					}
				}
			}
		case bson.M:
			if err := convertUUIDs(v); err != nil {
				return err
			}
		case map[string]interface{}:
			if err := convertUUIDs(bson.M(v)); err != nil {
				return err
			}
		default:
			continue
		}
	}
	return nil
}
