# Pathway Tutorial: Building a Social Network

This tutorial demonstrates how to build a simple social network application using the Pathway graph database. We will cover:

1. Setting up the database
2. Defining a schema-less data model
3. Seeding data (Users, Posts, interactions)
4. Running complex graph queries using the Fluid Interface

## 1. Setup

First, import the necessary packages:

```go
package main

import (
    "context"
    "fmt"
    "log"

    "github.com/google/uuid"
    "github.com/npclaudiu/pathway"
)

func main() {
    // Open an in-memory database for this tutorial
    db, err := pathway.Open(":memory:")
    if err != nil {
        log.Fatal(err)
    }
    defer db.Close()
    
    ctx := context.Background()
    // ...
}
```

## 2. Data Model

Pathway is schema-less, but conceptually we will model:

* **Nodes**: `User`, `Post`, `Comment`
* **Edges**:
  * `FOLLOWS` (User -> User)
  * `POSTED` (User -> Post)
  * `LIKED` (User -> Post)
  * `COMMENTED` (User -> Comment)
  * `ON` (Comment -> Post)

## 3. Seeding Data

We use `db.Update` for write transactions.

```go
func seedData(ctx context.Context, db *pathway.Database) (map[string]uuid.UUID, map[string]uuid.UUID, error) {
    users := make(map[string]uuid.UUID)
    posts := make(map[string]uuid.UUID)

    err := db.Update(ctx, func(tx *pathway.Tx) error {
        // 1. Create Users
        names := []string{"Alice", "Bob", "Charlie", "Dave", "Eve"}
        for _, name := range names {
            id := uuid.New()
            users[name] = id
            
            // Create Node
            if err := tx.PutNode(id, "User"); err != nil {
                return err
            }
            // Set Properties
            if err := tx.SetProperties(id, map[string]interface{}{
                "username": name,
                "age":      25,
            }); err != nil {
                return err
            }
        }

        // 2. Create Posts
        post1 := uuid.New()
        posts["AliceIntro"] = post1
        tx.PutNode(post1, "Post")
        tx.SetProperties(post1, map[string]interface{}{"content": "Hello World"})

        post2 := uuid.New()
        posts["BobUpdate"] = post2
        tx.PutNode(post2, "Post")
        tx.SetProperties(post2, map[string]interface{}{"content": "Bob is here"})

        // 3. Create Edges
        // Alice -> FOLLOWS -> Bob
        tx.PutEdge(users["Alice"], users["Bob"], "FOLLOWS")
        // Alice -> FOLLOWS -> Charlie
        tx.PutEdge(users["Alice"], users["Charlie"], "FOLLOWS")
        // Bob -> FOLLOWS -> Charlie
        tx.PutEdge(users["Bob"], users["Charlie"], "FOLLOWS")
        
        // Alice -> POSTED -> AliceIntro
        tx.PutEdge(users["Alice"], posts["AliceIntro"], "POSTED")
        
        // Bob -> LIKED -> AliceIntro
        tx.PutEdge(users["Bob"], posts["AliceIntro"], "LIKED")

        return nil
    })
    
    return users, posts, err
}
```

## 4. Querying

Pathway provides a **Fluid Traversal Interface** similar to Gremlin.

### 4.1 Find Friends of Alice

```go
func findFriends(db *pathway.Database, aliceID uuid.UUID) {
    g := pathway.NewTraversalSource(db)
    
    // Query: Start at Alice -> Outgoing "FOLLOWS" edges -> Neighbor Nodes
    results, err := g.V(aliceID.String()).Out("FOLLOWS").ToList()
    if err != nil {
        log.Fatal(err)
    }

    fmt.Printf("Alice follows %d people:\n", len(results))
    for _, r := range results {
        if m, ok := r.(map[string]interface{}); ok {
            fmt.Printf("- %v (Label: %s)\n", m["id"], m["label"])
        }
    }
}
```

### 4.2 Friends of Friends (2-Hop)

Find people Alice follows, and then who *they* follow.

```go
results, _ := g.V(aliceID.String()).
    Out("FOLLOWS"). // 1st Hop (Friends)
    Out("FOLLOWS"). // 2nd Hop (Friends of Friends)
    ToList()
```

### 4.3 Who Liked Alice's Post? (Incoming Edges)

Traverse backwards using `In()`.

```go
// Start at Post -> Incoming "LIKED" edge -> User
results, _ := g.V(postID.String()).
    In("LIKED").
    ToList()
```

### 4.4 Find Posts by Friends

Complex traversal combining multiple edge types.

```go
// Alice -> (Follows) -> Friends -> (Posted) -> Posts
results, _ := g.V(aliceID.String()).
    Out("FOLLOWS").
    Out("POSTED").
    ToList()
```

## Running the Code

You can see these examples in action by running the test suite:

```bash
go test ./tests/social_network_test.go -v
```
