package tests

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/npclaudiu/pathway"
)

// Social Network Data Model Test
//
// This test simulates a small social network to validate the fluid query interface.
//
// Data Model:
// Nodes:
// - User {username: string, age: int}
// - Post {content: string, created_at: string}
// - Comment {text: string}
//
// Edges:
// - FOLLOWS (User -> User)
// - POSTED (User -> Post)
// - LIKED (User -> Post)
// - COMMENTED (User -> Comment)
// - ON (Comment -> Post)

func TestSocialNetworkQueries(t *testing.T) {
	// 1. Setup Database
	db, err := pathway.Open(":memory:")
	if err != nil {
		t.Fatalf("Failed to open db: %v", err)
	}
	defer db.Close()

	ctx := context.Background()

	// 2. Seed Data
	// Users
	users := map[string]uuid.UUID{
		"Alice":   uuid.New(),
		"Bob":     uuid.New(),
		"Charlie": uuid.New(),
		"Dave":    uuid.New(),
		"Eve":     uuid.New(),
	}

	// Posts
	posts := map[string]uuid.UUID{
		"AliceIntro": uuid.New(),
		"BobUpdate":  uuid.New(),
		"CharliePic": uuid.New(),
	}

	err = db.Update(ctx, func(tx *pathway.Tx) error {
		// Create Users
		for name, id := range users {
			if err := tx.PutNode(id, "User"); err != nil {
				return err
			}
			if err := tx.SetProperties(id, map[string]interface{}{
				"username": name,
				"age":      20 + len(name)*2, // specific but arbitrary
			}); err != nil {
				return err
			}
		}

		// Create Posts
		if err := tx.PutNode(posts["AliceIntro"], "Post"); err != nil {
			return err
		}
		if err := tx.SetProperties(posts["AliceIntro"], map[string]interface{}{"content": "Hello World", "likes": 0}); err != nil {
			return err
		}

		if err := tx.PutNode(posts["BobUpdate"], "Post"); err != nil {
			return err
		}
		if err := tx.SetProperties(posts["BobUpdate"], map[string]interface{}{"content": "Bob is here", "likes": 5}); err != nil {
			return err
		}

		if err := tx.PutNode(posts["CharliePic"], "Post"); err != nil {
			return err
		}
		if err := tx.SetProperties(posts["CharliePic"], map[string]interface{}{"content": "Sunset", "likes": 100}); err != nil {
			return err
		}

		// Create Edges: FOLLOWS
		// Alice -> Bob, Charlie
		// Bob -> Charlie
		// Charlie -> Dave
		// Dave -> Eve
		// Eve -> Alice (Closing the loop)
		if _, err := tx.PutEdge(users["Alice"], users["Bob"], "FOLLOWS"); err != nil {
			return err
		}
		if _, err := tx.PutEdge(users["Alice"], users["Charlie"], "FOLLOWS"); err != nil {
			return err
		}
		if _, err := tx.PutEdge(users["Bob"], users["Charlie"], "FOLLOWS"); err != nil {
			return err
		}
		if _, err := tx.PutEdge(users["Charlie"], users["Dave"], "FOLLOWS"); err != nil {
			return err
		}
		if _, err := tx.PutEdge(users["Dave"], users["Eve"], "FOLLOWS"); err != nil {
			return err
		}
		if _, err := tx.PutEdge(users["Eve"], users["Alice"], "FOLLOWS"); err != nil {
			return err
		}

		// Create Edges: POSTED
		if _, err := tx.PutEdge(users["Alice"], posts["AliceIntro"], "POSTED"); err != nil {
			return err
		}
		if _, err := tx.PutEdge(users["Bob"], posts["BobUpdate"], "POSTED"); err != nil {
			return err
		}
		if _, err := tx.PutEdge(users["Charlie"], posts["CharliePic"], "POSTED"); err != nil {
			return err
		}

		// Create Edges: LIKED
		// Bob liked Alice's post
		if _, err := tx.PutEdge(users["Bob"], posts["AliceIntro"], "LIKED"); err != nil {
			return err
		}
		// Dave liked Charlie's post
		if _, err := tx.PutEdge(users["Dave"], posts["CharliePic"], "LIKED"); err != nil {
			return err
		}

		return nil
	})
	if err != nil {
		t.Fatalf("Failed to seed data: %v", err)
	}

	// DEBUG: Verify Alice exists explicitly
	{
		rtx, err := db.NewReadTx(ctx)
		if err != nil {
			t.Fatalf("Failed to start read tx: %v", err)
		}
		lbl, exists, err := rtx.GetNode(users["Alice"])
		if err != nil {
			t.Fatalf("GetNode error: %v", err)
		}
		if !exists {
			t.Fatalf("Alice node not found in DB!")
		}
		if lbl != "User" {
			t.Errorf("Expected Alice label User, got %s", lbl)
		}
		rtx.Close()
	}

	// 3. Validate Queries using Fluid Interface

	// scenario 1: Find friends of Alice (OutEdges)
	t.Run("FindFriends", func(t *testing.T) {
		g := pathway.NewTraversalSource(db)
		results, err := g.
			V(users["Alice"].String()).
			Out("FOLLOWS").
			ToList()

		if err != nil {
			t.Fatalf("Query failed: %v", err)
		}

		if len(results) != 2 {
			t.Errorf("Expected 2 friends, got %d", len(results))
		}
		// Expect Bob and Charlie
		found := make(map[uuid.UUID]bool)
		for _, r := range results {
			if m, ok := r.(map[string]interface{}); ok {
				if id, ok := m["id"].(uuid.UUID); ok {
					found[id] = true
				}
			}
		}
		if !found[users["Bob"]] || !found[users["Charlie"]] {
			t.Errorf("Expected Bob and Charlie, got IDs: %v", results)
		}
	})

	// Scenario 2: Find Friends of Friends (2 hops)
	// Alice -> [Bob, Charlie] -> [Charlie, Dave]
	// Using strict traversal: Alice->Bob->Charlie and Alice->Charlie->Dave
	t.Run("FindFriendsOfFriends", func(t *testing.T) {
		g := pathway.NewTraversalSource(db)
		results, err := g.
			V(users["Alice"].String()).
			Out("FOLLOWS"). // Friends
			Out("FOLLOWS"). // FOAF
			ToList()

		if err != nil {
			t.Fatalf("Query failed: %v", err)
		}

		// Verify we got Charlie and Dave
		foundNames := 0
		for _, r := range results {
			if m, ok := r.(map[string]interface{}); ok {
				if id, ok := m["id"].(uuid.UUID); ok {
					if id == users["Charlie"] {
						foundNames++
					} else if id == users["Dave"] {
						foundNames++
					}
				}
			}
		}

		if foundNames < 2 {
			t.Logf("Results: %+v", results)
			t.Errorf("Expected at least Charlie and Dave found via hops")
		}
	})

	// Scenario 3: Find Posts by Friends
	// Alice -> (Friends) -> POSTED -> (Post)
	t.Run("FindPostsByFriends", func(t *testing.T) {
		g := pathway.NewTraversalSource(db)
		results, err := g.
			V(users["Alice"].String()).
			Out("FOLLOWS").
			Out("POSTED").
			ToList()

		if err != nil {
			t.Fatalf("Query failed: %v", err)
		}

		// Expect 2 posts (BobUpdate, CharliePic)
		if len(results) != 2 {
			t.Errorf("Expected 2 posts, got %d", len(results))
		}
	})

	// Scenario 4: Who liked Alice's Intro?
	// AliceIntro <- LIKED <- User
	t.Run("WhoLikedAlicePost", func(t *testing.T) {
		g := pathway.NewTraversalSource(db)
		results, err := g.
			V(posts["AliceIntro"].String()).
			In("LIKED").
			ToList()

		if err != nil {
			t.Fatalf("Query failed: %v", err)
		}

		if len(results) != 1 {
			t.Fatalf("Expected 1 liker, got %d", len(results))
		}
		// Expect Bob
		if m, ok := results[0].(map[string]interface{}); ok {
			if id, ok := m["id"].(uuid.UUID); ok {
				if id != users["Bob"] {
					t.Errorf("Expected Bob to like the post, got %v", id)
				}
			} else {
				t.Errorf("Result ID not UUID: %v", results[0])
			}
		} else {
			t.Errorf("Result not a map: %v", results[0])
		}
	})
}
