package pathway

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
)

type closeTrackingNodeIterator struct {
	NodeIterator
	closed bool
}

type gatedNodeIterator struct {
	NodeIterator
	started chan struct{}
	proceed chan struct{}
	once    sync.Once
	closed  bool
}

func (it *gatedNodeIterator) Next() bool {
	valid := it.NodeIterator.Next()
	if valid {
		it.once.Do(func() {
			close(it.started)
			<-it.proceed
		})
	}
	return valid
}

func (it *gatedNodeIterator) Close() error {
	it.closed = true
	return it.NodeIterator.Close()
}

func (it *closeTrackingNodeIterator) Close() error {
	it.closed = true
	return it.NodeIterator.Close()
}

func TestQuery_VUsesTypedCallerOwnedIDs(t *testing.T) {
	db, _ := Open(":memory:")
	defer closeTestResource(t, db)

	id := uuid.New()
	if err := db.Update(context.Background(), func(tx *Tx) error {
		return tx.PutNode(id, "Thing")
	}); err != nil {
		t.Fatal(err)
	}

	g := NewTraversalSource(db)
	ids := []uuid.UUID{id}
	pipeline := g.V(ids...)
	ids[0] = uuid.New()
	results, err := pipeline.ToNodes()
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].ID != id {
		t.Fatalf("typed V results = %#v, want node %s", results, id)
	}
}

func TestQuery_VStringsParsesValidIDsAndRejectsMalformedIDs(t *testing.T) {
	db, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer closeTestResource(t, db)

	id := uuid.New()
	if err := db.Update(context.Background(), func(tx *Tx) error {
		return tx.PutNode(id, "Thing")
	}); err != nil {
		t.Fatal(err)
	}

	g := NewTraversalSource(db)
	pipeline, err := g.VStrings(id.String())
	if err != nil {
		t.Fatal(err)
	}
	results, err := pipeline.ToNodes()
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].ID != id {
		t.Fatalf("VStrings results = %#v, want node %s", results, id)
	}

	pipeline, err = g.VStrings(id.String(), "not-a-uuid")
	if pipeline != nil {
		t.Fatal("VStrings returned a partial pipeline for malformed input")
	}
	if !errors.Is(err, ErrInvalidNodeID) {
		t.Fatalf("VStrings error = %v, want ErrInvalidNodeID", err)
	}
}

func TestQuery_ToList_NilDatabase(t *testing.T) {
	// g with nil db
	g := NewTraversalSource(nil)
	q := g.V()

	_, err := q.ToList()
	if err != ErrInvalidDatabase {
		t.Errorf("expected ErrInvalidDatabase, got %v", err)
	}
}

func TestQuery_EachStreamsResults(t *testing.T) {
	db, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer closeTestResource(t, db)

	const nodeCount = 1_000
	if err := db.Update(context.Background(), func(tx *Tx) error {
		for range nodeCount {
			if err := tx.PutNode(uuid.New(), "Node"); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	count := 0
	err = NewTraversalSource(db).V().EachNode(context.Background(), func(Node) error {
		count++
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if count != nodeCount {
		t.Fatalf("callback count = %d, want %d", count, nodeCount)
	}
}

func TestQuery_EachCallbackErrorClosesIteratorChain(t *testing.T) {
	db, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer closeTestResource(t, db)

	if err := db.Update(context.Background(), func(tx *Tx) error {
		for range 3 {
			if err := tx.PutNode(uuid.New(), "Node"); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	var tracked *closeTrackingNodeIterator
	pipeline := &TraversalPipeline{
		db: db,
		steps: []Step{
			func(tx *Tx, _ Iterator) Iterator {
				tracked = &closeTrackingNodeIterator{NodeIterator: tx.ScanNodes()}
				return tracked
			},
			func(_ *Tx, previous Iterator) Iterator {
				return newIDIterator(previous)
			},
		},
	}

	stopErr := errors.New("stop streaming")
	callbacks := 0
	err = pipeline.Each(context.Background(), func(interface{}) error {
		callbacks++
		return stopErr
	})
	if !errors.Is(err, stopErr) {
		t.Fatalf("Each error = %v, want %v", err, stopErr)
	}
	if callbacks != 1 {
		t.Fatalf("callback count = %d, want 1", callbacks)
	}
	if tracked == nil || !tracked.closed {
		t.Fatal("callback termination did not close the iterator chain")
	}
}

func TestQuery_EachPropagatesCancellationAndContextToHooks(t *testing.T) {
	type contextKey struct{}
	var startContext, endContext context.Context
	var endErr error
	db, err := OpenWithOptions(":memory:", Options{
		OnQueryStart: func(ctx context.Context, _ string) {
			startContext = ctx
		},
		OnQueryEnd: func(ctx context.Context, _ string, _ time.Duration, err error) {
			endContext = ctx
			endErr = err
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer closeTestResource(t, db)

	if err := db.Update(context.Background(), func(tx *Tx) error {
		for range 3 {
			if err := tx.PutNode(uuid.New(), "Node"); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.WithValue(context.Background(), contextKey{}, "caller"))
	callbacks := 0
	err = NewTraversalSource(db).V().Each(ctx, func(interface{}) error {
		callbacks++
		cancel()
		return nil
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Each error = %v, want context.Canceled", err)
	}
	if callbacks != 1 {
		t.Fatalf("callback count = %d, want 1", callbacks)
	}
	if startContext != ctx || endContext != ctx {
		t.Fatal("query hooks did not receive the caller context")
	}
	if startContext.Value(contextKey{}) != "caller" {
		t.Fatal("query hook context lost caller values")
	}
	if !errors.Is(endErr, context.Canceled) {
		t.Fatalf("OnQueryEnd error = %v, want context.Canceled", endErr)
	}
}

func TestQuery_ToListContextPropagatesCallerContextToHooks(t *testing.T) {
	type contextKey struct{}
	var startContext, endContext context.Context
	var endErr error
	db, err := OpenWithOptions(":memory:", Options{
		OnQueryStart: func(ctx context.Context, _ string) {
			startContext = ctx
		},
		OnQueryEnd: func(ctx context.Context, _ string, _ time.Duration, err error) {
			endContext = ctx
			endErr = err
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer closeTestResource(t, db)

	ctx, cancel := context.WithCancel(context.WithValue(context.Background(), contextKey{}, "to-list"))
	cancel()
	results, err := NewTraversalSource(db).V().ToListContext(ctx)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("ToListContext error = %v, want context.Canceled", err)
	}
	if results != nil {
		t.Fatalf("cancelled results = %#v, want nil", results)
	}
	if startContext != ctx || endContext != ctx {
		t.Fatal("query hooks did not receive the ToListContext caller context")
	}
	if startContext.Value(contextKey{}) != "to-list" {
		t.Fatal("query hook context lost caller values")
	}
	if !errors.Is(endErr, context.Canceled) {
		t.Fatalf("OnQueryEnd error = %v, want context.Canceled", endErr)
	}
}

func TestQuery_ToListContextCancelsNodeScanAndClosesIterator(t *testing.T) {
	db, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer closeTestResource(t, db)
	if err := db.Update(context.Background(), func(tx *Tx) error {
		return tx.PutNode(uuid.New(), "Present")
	}); err != nil {
		t.Fatal(err)
	}

	started := make(chan struct{})
	proceed := make(chan struct{})
	var scan *gatedNodeIterator
	pipeline := &TraversalPipeline{
		db: db,
		steps: []Step{func(tx *Tx, _ Iterator) Iterator {
			scan = &gatedNodeIterator{
				NodeIterator: tx.ScanNodes(),
				started:      started,
				proceed:      proceed,
			}
			return scan
		}},
	}
	pipeline.HasLabel("Missing")

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := pipeline.ToListContext(ctx)
		done <- err
	}()
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("node scan did not start")
	}
	cancel()
	close(proceed)
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("scan error = %v, want context.Canceled", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("cancelled node scan did not stop promptly")
	}
	if scan == nil || !scan.closed {
		t.Fatal("cancelled node scan did not close its iterator")
	}
}

func TestQuery_ToListContextCancelsRepeat(t *testing.T) {
	db, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer closeTestResource(t, db)

	id := uuid.New()
	if err := db.Update(context.Background(), func(tx *Tx) error {
		if err := tx.PutNode(id, "Node"); err != nil {
			return err
		}
		_, err := tx.PutEdge(id, id, "LOOP")
		return err
	}); err != nil {
		t.Fatal(err)
	}

	started := make(chan struct{})
	proceed := make(chan struct{})
	var once sync.Once
	pipeline := NewTraversalSource(db).V(id).Repeat(func(p *TraversalPipeline) *TraversalPipeline {
		return p.Out("LOOP")
	}).Until(func(Node) bool {
		once.Do(func() {
			close(started)
			<-proceed
		})
		return false
	})

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := pipeline.ToListContext(ctx)
		done <- err
	}()
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("repeat traversal did not start")
	}
	cancel()
	close(proceed)
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("repeat error = %v, want context.Canceled", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("cancelled repeat did not stop promptly")
	}
}

func TestQuery_Path_Check(t *testing.T) {
	db, _ := Open(":memory:")
	defer closeTestResource(t, db)
	ctx := context.Background()

	u1 := uuid.New()
	u2 := uuid.New()
	var edgeID uuid.UUID
	if err := db.Update(ctx, func(tx *Tx) error {
		if err := tx.PutNode(u1, "A"); err != nil {
			return err
		}
		if err := tx.PutNode(u2, "B"); err != nil {
			return err
		}
		var err error
		edgeID, err = tx.PutEdge(u1, u2, "LINK")
		return err
	}); err != nil {
		t.Fatal(err)
	}

	g := NewTraversalSource(db)
	res, err := g.V(u1).Out("LINK").Path().ToPaths()
	if err != nil {
		t.Fatalf("Path query failed: %v", err)
	}
	if len(res) != 1 {
		t.Fatalf("expected one path, got %d", len(res))
	}
	path := res[0]
	want := Path{
		{Kind: PathNode, ID: u1, Label: "A"},
		{Kind: PathEdge, ID: edgeID, Label: "LINK", Other: u2},
		{Kind: PathNode, ID: u2, Label: "B"},
	}
	if !reflect.DeepEqual(path, want) {
		t.Fatalf("path = %#v, want %#v", path, want)
	}

	// Materialized paths do not share mutable backing storage with later reads.
	path[0].Label = "mutated"
	again, err := g.V(u1).Out("LINK").Path().ToPaths()
	if err != nil {
		t.Fatal(err)
	}
	if again[0][0].Label != "A" {
		t.Fatal("path result was mutated through shared backing storage")
	}
}

func TestQuery_HasLabel(t *testing.T) {
	db, _ := Open(":memory:")
	defer closeTestResource(t, db)
	ctx := context.Background()

	u1 := uuid.New()
	u2 := uuid.New()
	if err := db.Update(ctx, func(tx *Tx) error {
		if err := tx.PutNode(u1, "Person"); err != nil {
			return err
		}
		if err := tx.PutNode(u2, "Robot"); err != nil {
			return err
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	g := NewTraversalSource(db)

	// HasLabel("Robot")
	res, err := g.V().HasLabel("Robot").ToNodes()
	if err != nil {
		t.Fatal(err)
	}
	if len(res) != 1 {
		t.Errorf("expected 1 robot, got %d", len(res))
	}
	node := res[0]
	if node.ID != u2 || node.Label != "Robot" {
		t.Errorf("result = %#v, want Robot node %s", node, u2)
	}
}

func TestQuery_TypedTerminalsReturnOwnedElements(t *testing.T) {
	db, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer closeTestResource(t, db)

	ctx := context.Background()
	source, target := uuid.New(), uuid.New()
	var edgeID uuid.UUID
	if err := db.Update(ctx, func(tx *Tx) error {
		if err := tx.PutNode(source, "Source"); err != nil {
			return err
		}
		if err := tx.PutNode(target, "Target"); err != nil {
			return err
		}
		edgeID, err = tx.PutEdge(source, target, "LINK")
		return err
	}); err != nil {
		t.Fatal(err)
	}

	nodes, err := NewTraversalSource(db).V(source).Out("LINK").ToNodes()
	if err != nil {
		t.Fatal(err)
	}
	if want := (Node{ID: target, Label: "Target"}); len(nodes) != 1 || nodes[0] != want {
		t.Fatalf("nodes = %#v, want [%#v]", nodes, want)
	}

	edges, err := (&TraversalPipeline{
		db: db,
		steps: []Step{func(tx *Tx, _ Iterator) Iterator {
			return tx.OutEdges(source, "LINK")
		}},
	}).ToEdges()
	if err != nil {
		t.Fatal(err)
	}
	if want := (Edge{ID: edgeID, Other: target, Label: "LINK"}); len(edges) != 1 || edges[0] != want {
		t.Fatalf("edges = %#v, want [%#v]", edges, want)
	}

	if err := db.Update(ctx, func(tx *Tx) error { return tx.PutNode(target, "Renamed") }); err != nil {
		t.Fatal(err)
	}
	if got := nodes[0].Label; got != "Target" {
		t.Fatalf("materialized label changed after relabel: %q", got)
	}
}

func TestQuery_TypedTerminalRejectsWrongResultShape(t *testing.T) {
	db, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer closeTestResource(t, db)

	id := uuid.New()
	if err := db.Update(context.Background(), func(tx *Tx) error {
		return tx.PutNode(id, "Node")
	}); err != nil {
		t.Fatal(err)
	}

	g := NewTraversalSource(db)
	results, err := g.V(id).IDs().ToNodes()
	if results != nil {
		t.Fatalf("mismatched results = %#v, want nil", results)
	}
	if !errors.Is(err, ErrTraversalResult) {
		t.Fatalf("mismatched result error = %v, want ErrTraversalResult", err)
	}
	emptyResults, err := g.V(uuid.New()).IDs().ToNodes()
	if emptyResults != nil || !errors.Is(err, ErrTraversalResult) {
		t.Fatalf("empty mismatched traversal = %#v, %v; want nil, ErrTraversalResult", emptyResults, err)
	}
	if err := g.V(id).EachNode(context.Background(), nil); err == nil {
		t.Fatal("EachNode accepted a nil callback")
	}
}

func TestQuery_In(t *testing.T) {
	db, _ := Open(":memory:")
	defer closeTestResource(t, db)
	ctx := context.Background()

	u1 := uuid.New()
	u2 := uuid.New()
	if err := db.Update(ctx, func(tx *Tx) error {
		if err := tx.PutNode(u1, "A"); err != nil {
			return err
		}
		if err := tx.PutNode(u2, "B"); err != nil {
			return err
		}
		if _, err := tx.PutEdge(u1, u2, "LINK"); err != nil {
			return err
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	g := NewTraversalSource(db)

	// u2 <- In("LINK") == u1
	res, err := g.V(u2).In("LINK").ToNodes()
	if err != nil {
		t.Fatal(err)
	}
	if len(res) != 1 {
		t.Errorf("expected 1 result, got %d", len(res))
	}
}

func TestQuery_Repeat(t *testing.T) {
	db, _ := Open(":memory:")
	defer closeTestResource(t, db)
	ctx := context.Background()

	// Chain: A -> B -> C
	a := uuid.New()
	b := uuid.New()
	c := uuid.New()
	if err := db.Update(ctx, func(tx *Tx) error {
		if err := tx.PutNode(a, "Node"); err != nil {
			return err
		}
		if err := tx.PutNode(b, "Node"); err != nil {
			return err
		}
		if err := tx.PutNode(c, "Node"); err != nil {
			return err
		}
		if _, err := tx.PutEdge(a, b, "NEXT"); err != nil {
			return err
		}
		if _, err := tx.PutEdge(b, c, "NEXT"); err != nil {
			return err
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	g := NewTraversalSource(db)

	// Repeat(Out).Times(2)
	res, err := g.V(a).Repeat(func(p *TraversalPipeline) *TraversalPipeline {
		return p.Out("NEXT")
	}).Times(2).ToNodes()

	if err != nil {
		t.Fatal(err)
	}
	if len(res) != 1 {
		t.Fatalf("expected 1 result (C), got %d", len(res))
	}
	if res[0].ID != c {
		t.Fatalf("repeat result = %#v, want node %s", res[0], c)
	}

	var visited []Node
	res, err = g.V(a).Repeat(func(p *TraversalPipeline) *TraversalPipeline {
		return p.Out("NEXT")
	}).Until(func(node Node) bool {
		visited = append(visited, node)
		return node.ID == c
	}).ToNodes()
	if err != nil {
		t.Fatal(err)
	}
	if len(res) != 1 || res[0].ID != c {
		t.Fatalf("Until result = %#v, want node %s", res, c)
	}
	if len(visited) != 3 || visited[0].ID != a || visited[2].ID != c {
		t.Fatalf("Until predicate nodes = %#v, want A through C", visited)
	}

	res, err = g.V(a).Repeat(func(p *TraversalPipeline) *TraversalPipeline {
		return p.Out("NEXT")
	}).Times(3).Until(func(node Node) bool {
		return node.ID == c
	}).Emit().ToNodes()
	if err != nil {
		t.Fatal(err)
	}
	if want := []Node{{ID: a, Label: "Node"}, {ID: b, Label: "Node"}, {ID: c, Label: "Node"}}; !reflect.DeepEqual(res, want) {
		t.Fatalf("combined repeat modifiers = %#v, want %#v", res, want)
	}
}

func TestQuery_RepeatRequiresValidConfiguration(t *testing.T) {
	db, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer closeTestResource(t, db)

	g := NewTraversalSource(db)
	body := func(p *TraversalPipeline) *TraversalPipeline { return p.Out("NEXT") }
	pred := func(Node) bool { return false }
	tests := []struct {
		name     string
		pipeline func() *TraversalPipeline
	}{
		{"unterminated", func() *TraversalPipeline { return g.V().Repeat(body) }},
		{"emit is not termination", func() *TraversalPipeline { return g.V().Repeat(body).Emit() }},
		{"nil body", func() *TraversalPipeline { return g.V().Repeat(nil).Times(1) }},
		{"nil predicate", func() *TraversalPipeline { return g.V().Repeat(body).Until(nil) }},
		{"zero times", func() *TraversalPipeline { return g.V().Repeat(body).Times(0) }},
		{"negative times", func() *TraversalPipeline { return g.V().Repeat(body).Times(-1) }},
		{"until without repeat", func() *TraversalPipeline { return g.V().Until(pred) }},
		{"times without repeat", func() *TraversalPipeline { return g.V().Times(1) }},
		{"emit without repeat", func() *TraversalPipeline { return g.V().Emit() }},
		{"allow unbounded without repeat", func() *TraversalPipeline { return g.V().AllowUnboundedRepeat() }},
		{"duplicate until", func() *TraversalPipeline { return g.V().Repeat(body).Until(pred).Until(pred) }},
		{"duplicate times", func() *TraversalPipeline { return g.V().Repeat(body).Times(1).Times(2) }},
		{"duplicate emit", func() *TraversalPipeline { return g.V().Repeat(body).Times(1).Emit().Emit() }},
		{"duplicate allow unbounded", func() *TraversalPipeline {
			return g.V().Repeat(body).AllowUnboundedRepeat().AllowUnboundedRepeat()
		}},
		{"times then unbounded", func() *TraversalPipeline {
			return g.V().Repeat(body).Times(1).AllowUnboundedRepeat()
		}},
		{"unbounded then times", func() *TraversalPipeline {
			return g.V().Repeat(body).AllowUnboundedRepeat().Times(1)
		}},
		{"until then unbounded", func() *TraversalPipeline {
			return g.V().Repeat(body).Until(pred).AllowUnboundedRepeat()
		}},
		{"unbounded then until", func() *TraversalPipeline {
			return g.V().Repeat(body).AllowUnboundedRepeat().Until(pred)
		}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := test.pipeline().ToList()
			if !errors.Is(err, ErrInvalidRepeat) {
				t.Fatalf("error = %v, want ErrInvalidRepeat", err)
			}
		})
	}
}

func TestQuery_RepeatModifiersCloseAfterTraversalSteps(t *testing.T) {
	db, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer closeTestResource(t, db)

	g := NewTraversalSource(db)
	body := func(p *TraversalPipeline) *TraversalPipeline { return p.Out() }
	steps := []struct {
		name string
		step func(*TraversalPipeline) *TraversalPipeline
	}{
		{"Out", func(p *TraversalPipeline) *TraversalPipeline { return p.Out() }},
		{"In", func(p *TraversalPipeline) *TraversalPipeline { return p.In() }},
		{"HasLabel", func(p *TraversalPipeline) *TraversalPipeline { return p.HasLabel("Node") }},
		{"Path", func(p *TraversalPipeline) *TraversalPipeline { return p.Path() }},
		{"IDs", func(p *TraversalPipeline) *TraversalPipeline { return p.IDs() }},
		{"Values", func(p *TraversalPipeline) *TraversalPipeline { return p.Values("name") }},
	}

	for _, step := range steps {
		t.Run(step.name, func(t *testing.T) {
			pipeline := g.V().Repeat(body).Times(1)
			pipeline = step.step(pipeline).Emit()
			_, err := pipeline.ToList()
			if !errors.Is(err, ErrInvalidRepeat) {
				t.Fatalf("error = %v, want ErrInvalidRepeat", err)
			}
		})
	}
}

func TestQuery_RepeatBodyConfigurationIsValidated(t *testing.T) {
	db, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer closeTestResource(t, db)

	id := uuid.New()
	if err := db.Update(context.Background(), func(tx *Tx) error {
		return tx.PutNode(id, "Node")
	}); err != nil {
		t.Fatal(err)
	}
	g := NewTraversalSource(db)

	tests := []struct {
		name string
		body func(*TraversalPipeline) *TraversalPipeline
	}{
		{"nil pipeline", func(*TraversalPipeline) *TraversalPipeline { return nil }},
		{"unterminated nested repeat", func(p *TraversalPipeline) *TraversalPipeline {
			return p.Repeat(func(nested *TraversalPipeline) *TraversalPipeline { return nested.Out() })
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := g.V(id).Repeat(test.body).Times(1).ToNodes()
			if !errors.Is(err, ErrInvalidRepeat) {
				t.Fatalf("error = %v, want ErrInvalidRepeat", err)
			}
		})
	}
}

func TestQuery_RepeatAllowsExplicitUnboundedExecution(t *testing.T) {
	db, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer closeTestResource(t, db)

	id := uuid.New()
	if err := db.Update(context.Background(), func(tx *Tx) error {
		if err := tx.PutNode(id, "Node"); err != nil {
			return err
		}
		_, err := tx.PutEdge(id, id, "LOOP")
		return err
	}); err != nil {
		t.Fatal(err)
	}

	stop := errors.New("stop repeat")
	count := 0
	err = NewTraversalSource(db).V(id).
		Repeat(func(p *TraversalPipeline) *TraversalPipeline { return p.Out("LOOP") }).
		Emit().
		AllowUnboundedRepeat().
		EachNode(context.Background(), func(Node) error {
			count++
			if count == 3 {
				return stop
			}
			return nil
		})
	if !errors.Is(err, stop) {
		t.Fatalf("error = %v, want stopping callback error", err)
	}
	if count != 3 {
		t.Fatalf("emitted %d nodes, want 3", count)
	}
}

func TestQuery_Values(t *testing.T) {
	db, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer closeTestResource(t, db)
	id := uuid.New()
	if err := db.Update(context.Background(), func(tx *Tx) error {
		if err := tx.PutNode(id, "Node"); err != nil {
			return err
		}
		return tx.SetProperties(id, map[string]interface{}{
			"name":   "Alice",
			"age":    30,
			"active": true,
		})
	}); err != nil {
		t.Fatal(err)
	}

	g := NewTraversalSource(db)
	res, err := g.V(id).Values("name").ToList()
	if err != nil {
		t.Fatal(err)
	}
	if len(res) != 1 || res[0] != "Alice" {
		t.Fatalf("single-key projection = %#v, want [Alice]", res)
	}

	res, err = g.V(id).Values("name", "missing", "age", "active").ToList()
	if err != nil {
		t.Fatal(err)
	}
	want := []interface{}{"Alice", int64(30), true}
	if !reflect.DeepEqual(res, want) {
		t.Fatalf("multi-key mixed projection = %#v, want %#v", res, want)
	}

	res, err = g.V(id).Values("missing").ToList()
	if err != nil {
		t.Fatal(err)
	}
	if len(res) != 0 {
		t.Fatalf("missing property should be omitted, got %#v", res)
	}
}

func TestQuery_IDsAvoidLabelMaterializationAcrossHops(t *testing.T) {
	db, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer closeTestResource(t, db)

	a, b, c := uuid.New(), uuid.New(), uuid.New()
	if err := db.Update(context.Background(), func(tx *Tx) error {
		for id, label := range map[uuid.UUID]string{a: "A", b: "B", c: "C"} {
			if err := tx.PutNode(id, label); err != nil {
				return err
			}
		}
		if _, err := tx.PutEdge(a, b, "NEXT"); err != nil {
			return err
		}
		if _, err := tx.PutEdge(b, c, "NEXT"); err != nil {
			return err
		}
		return tx.SetProperties(b, map[string]interface{}{"name": "middle"})
	}); err != nil {
		t.Fatal(err)
	}

	g := NewTraversalSource(db)
	oneHop, err := g.V(a).Out("NEXT").IDs().ToIDs()
	if err != nil {
		t.Fatal(err)
	}
	if want := []uuid.UUID{b}; !reflect.DeepEqual(oneHop, want) {
		t.Fatalf("one-hop IDs = %#v, want %#v", oneHop, want)
	}

	twoHops, err := g.V(a).Out("NEXT").Out("NEXT").IDs().ToIDs()
	if err != nil {
		t.Fatal(err)
	}
	if want := []uuid.UUID{c}; !reflect.DeepEqual(twoHops, want) {
		t.Fatalf("two-hop IDs = %#v, want %#v", twoHops, want)
	}

	values, err := g.V(a).Out("NEXT").Values("name").ToList()
	if err != nil {
		t.Fatal(err)
	}
	if want := []interface{}{"middle"}; !reflect.DeepEqual(values, want) {
		t.Fatalf("neighbor values = %#v, want %#v", values, want)
	}
}
