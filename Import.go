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
}
type Collection struct {
	Name        string `yaml:"name"`
	BatchSize   string `yaml:"batchSize"`
	MultiTenant bool   `yaml:"multiTenant"`
	Filter      string `yaml:"filter"`
	Upsert      bool   `yaml:"upsert"`
}
type Mirror struct {
	Configs     Config       `yaml:"config"`
	Collections []Collection `yaml:"collections"`
}

func (m *Mirror) LoadConfig(ctx context.Context) {
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
		m.Configs.DestinyClient.Connection, err = connectDb(ctx, m.Configs.SourceClient.ConnectionString, Destiny)
		if err != nil {
			log.Fatal(err)
		}
	}(&wg)
	wg.Wait()
}
func connectDb(ctx context.Context, connectionString string, direction Direction) (*mongo.Client, error) {
	if direction == Destiny && strings.Contains(connectionString, "mongodb.net") {
		panic("You can't connect to a mongodb atlas, the destiny database can't be a production database.")
	}
	log.Printf("Start connect mongodb %s", direction)
	source, err := mongo.Connect(ctx, options.Client().ApplyURI(connectionString))
	if err != nil {
		log.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := source.Ping(ctx, nil); err != nil {
		return nil, fmt.Errorf("failed to ping MongoDB -> %s: \n %s", direction, err)
	}

	log.Printf("Successfully connected to MongoDB! %s", direction)
	return source, nil
}

func (m *Mirror) LoadCollections(ctx context.Context) {
	wg := sync.WaitGroup{}
	for _, collection := range m.Collections {
		wg.Add(1)
		go func() {
			defer wg.Done()
			dbDestiny(ctx, m.Configs, collection)
		}()
	}
	wg.Wait()
}

func dbSource(ctx context.Context, db Config, collection Collection) *mongo.Cursor {
	sourceCollection := db.SourceClient.Connection.Database(db.SourceClient.Database).Collection(collection.Name)

	if collection.MultiTenant != false && len(db.Tenants) > 0 {
		collection.Filter = fmt.Sprintf(`{"tenant": {"$in": %s}}`, strings.Join(db.Tenants, ","))
	}

	var filter bson.M
	if err := json.Unmarshal([]byte(collection.Filter), &filter); err != nil {
		log.Fatal(err)
	}

	if err := convertUUIDs(filter); err != nil {
		log.Fatalf("Failed to convert UUIDs in filter for collection %s: %v", collection.Name, err)
	}
	count, err := sourceCollection.CountDocuments(ctx, filter)
	if err != nil {
		log.Fatal(err)
	}

	fmt.Printf("Total documents imported: %d\n", count)
	var findOptions *options.FindOptions

	if collection.BatchSize != "all" {
		i, err := strconv.Atoi(collection.BatchSize)
		if err != nil {
			log.Fatal("BatchSize must be a number or 'all'")
			panic(err)
		}
		findOptions.SetBatchSize(int32(i))
	}
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
	if collection.Upsert {
		for cursor.Next(ctx) {
			var document bson.M
			if err := cursor.Decode(&document); err != nil {
				log.Fatal(err)
			}
			_, err := destinyCollection.UpdateOne(
				ctx,
				bson.M{"_id": document["_id"]}, // Filtro baseado no ID do documento
				bson.M{"$set": document},
				options.Update().SetUpsert(true),
			)
			if err != nil {
				log.Fatal(err)
			}
			copiedCount += int64(len(documents))
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
		}
	}

	fmt.Printf("Collection %s imported successfully!", collection.Name)
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
		if str, ok := value.(string); ok && strings.Contains(str, "-") {
			if binary, err := uuidToBinary(str); err == nil {
				filter[key] = binary
			} else {
				return err
			}
		} else if subFilter, ok := value.(bson.M); ok {
			if err := convertUUIDs(subFilter); err != nil {
				return err
			}
		}
	}
	return nil
}
