# Pathway Tutorial: Building a Social Network

This tutorial demonstrates how to build a simple social network application using the Pathway graph database. We will cover:

1. Setting up the database
2. Defining a schema-less data model
3. Seeding data (Users, Posts, interactions)
4. Running graph queries using the fluent traversal API

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
            
            if err := tx.PutNode(id, "User"); err != nil {
                return err
            }
            if err := tx.SetProperties(id, map[string]any{
                "username": name,
                "age":      25,
            }); err != nil {
                return err
            }
        }

        // 2. Create Posts
        post1 := uuid.New()
        posts["AliceIntro"] = post1
        if err := tx.PutNode(post1, "Post"); err != nil {
            return err
        }
        if err := tx.SetProperties(post1, map[string]any{"content": "Hello World"}); err != nil {
            return err
        }

        post2 := uuid.New()
        posts["BobUpdate"] = post2
        if err := tx.PutNode(post2, "Post"); err != nil {
            return err
        }
        if err := tx.SetProperties(post2, map[string]any{"content": "Bob is here"}); err != nil {
            return err
        }

        // 3. Create Edges
        edges := []struct {
            from, to uuid.UUID
            label    string
        }{
            {users["Alice"], users["Bob"], "FOLLOWS"},
            {users["Alice"], users["Charlie"], "FOLLOWS"},
            {users["Bob"], users["Charlie"], "FOLLOWS"},
            {users["Alice"], posts["AliceIntro"], "POSTED"},
            {users["Bob"], posts["AliceIntro"], "LIKED"},
        }
        for _, edge := range edges {
            if _, err := tx.PutEdge(edge.from, edge.to, edge.label); err != nil {
                return err
            }
        }

        return nil
    })
    
    return users, posts, err
}
```

## 4. Querying

Pathway provides a fluent traversal interface inspired by Gremlin. Node results
are currently represented as `map[string]any`; property and path projections
have scalar and typed result forms, respectively.

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
        if m, ok := r.(map[string]any); ok {
            fmt.Printf("- %v (Label: %s)\n", m["id"], m["label"])
        }
    }
}
```

### 4.2 Friends of Friends (2-Hop)

Find people Alice follows, and then who *they* follow.

```go
results, err := g.V(aliceID.String()).
    Out("FOLLOWS"). // 1st Hop (Friends)
    Out("FOLLOWS"). // 2nd Hop (Friends of Friends)
    ToList()
if err != nil {
    log.Fatal(err)
}
```

### 4.3 Who Liked Alice's Post? (Incoming Edges)

Traverse backwards using `In()`.

```go
// Start at Post -> Incoming "LIKED" edge -> User
results, err := g.V(postID.String()).
    In("LIKED").
    ToList()
if err != nil {
    log.Fatal(err)
}
```

### 4.4 Find Posts by Friends

Complex traversal combining multiple edge types.

```go
// Alice -> (Follows) -> Friends -> (Posted) -> Posts
results, err := g.V(aliceID.String()).
    Out("FOLLOWS").
    Out("POSTED").
    ToList()
if err != nil {
    log.Fatal(err)
}
```

### 4.5 Project Property Values

`Values` emits one typed scalar for every requested property that exists. The
requested key order is preserved and missing properties are skipped.

```go
names, err := g.V(aliceID.String()).
    Out("FOLLOWS").
    Values("username").
    ToList()
if err != nil {
    log.Fatal(err)
}
```

### 4.6 Inspect the Traversed Path

`Path` returns a `pathway.Path`. Each entry has a `Kind`, `ID`, and `Label`;
edge entries also set `Other` to the endpoint reached by that step.

```go
paths, err := g.V(aliceID.String()).Out("FOLLOWS").Path().ToList()
if err != nil {
    log.Fatal(err)
}
for _, result := range paths {
    path := result.(pathway.Path)
    for _, element := range path {
        fmt.Printf("%s %s %s\n", element.Kind, element.Label, element.ID)
    }
}
```

## Running the Code

Run the complete example or the integration test suite:

```bash
go run ./examples/social_network
go test ./tests -v
```
