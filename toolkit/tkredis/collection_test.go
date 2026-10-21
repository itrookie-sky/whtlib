package tkredis

import (
	"errors"
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
)

func TestSetOperationsAndAtomicTTL(t *testing.T) {
	client, server := newTestClient(t)
	ctx := t.Context()
	args := []any{"a", "b", JSON(map[string]int{"x": 1})}
	count, err := client.SAdd(ctx, "set", args, 125*time.Millisecond)
	if err != nil || count != 3 || server.TTL("set") != 125*time.Millisecond {
		t.Fatalf("add=%d, ttl=%v, error=%v", count, server.TTL("set"), err)
	}
	_, unchanged := args[2].(JSONValue)
	if !unchanged {
		t.Fatal("SAdd changed member arguments")
	}
	exists, err := client.SIsMember(ctx, "set", "a", 250*time.Millisecond)
	if err != nil || !exists || server.TTL("set") != 250*time.Millisecond {
		t.Fatalf("member=%v, ttl=%v, error=%v", exists, server.TTL("set"), err)
	}
	members, err := client.SMembers(ctx, "set", 300*time.Millisecond)
	slices.Sort(members)
	if err != nil || !reflect.DeepEqual(members, []string{"a", "b", `{"x":1}`}) || server.TTL("set") != 300*time.Millisecond {
		t.Fatalf("members=%v, error=%v", members, err)
	}
	count, err = client.SCard(ctx, "set", 400*time.Millisecond)
	if err != nil || count != 3 || server.TTL("set") != 400*time.Millisecond {
		t.Fatalf("card=%d, error=%v", count, err)
	}
	count, err = client.SRem(ctx, "set", []any{"a"}, 500*time.Millisecond)
	if err != nil || count != 1 || server.TTL("set") != 500*time.Millisecond {
		t.Fatalf("remove=%d, error=%v", count, err)
	}
	members, err = client.SPop(ctx, "set", 1, 600*time.Millisecond)
	if err != nil || len(members) != 1 || server.TTL("set") != 600*time.Millisecond {
		t.Fatalf("pop=%v, error=%v", members, err)
	}
	members, err = client.SPop(ctx, "set", 10, 700*time.Millisecond)
	if err != nil || len(members) != 1 || server.Exists("set") {
		t.Fatalf("pop all=%v, error=%v", members, err)
	}
	members, err = client.SMembers(ctx, "missing", time.Second)
	if err != nil || len(members) != 0 || server.Exists("missing") {
		t.Fatalf("missing members=%v, error=%v", members, err)
	}
	_, err = client.SAdd(ctx, "invalid", []any{"x"}, -time.Second)
	if err == nil || server.Exists("invalid") {
		t.Fatal("negative TTL must fail before mutation")
	}
}

func TestSetUnionRequiresAllKeysBeforeRenewal(t *testing.T) {
	client, server := newTestClient(t)
	ctx := t.Context()
	_, err := client.SAdd(ctx, "one", []any{"a"}, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	members, err := client.SUnion(ctx, []string{"one", "two"}, 5*time.Second)
	if err != nil || !reflect.DeepEqual(members, []string{"a"}) || server.TTL("one") != time.Second {
		t.Fatalf("partial union=%v, ttl=%v, error=%v", members, server.TTL("one"), err)
	}
	_, err = client.SAdd(ctx, "two", []any{"b"}, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	members, err = client.SUnion(ctx, []string{"one", "two"}, 5*time.Second)
	slices.Sort(members)
	if err != nil || !reflect.DeepEqual(members, []string{"a", "b"}) || server.TTL("one") != 5*time.Second || server.TTL("two") != 5*time.Second {
		t.Fatalf("union=%v, error=%v", members, err)
	}
}

func TestSortedSetRangesPaginationAndMutations(t *testing.T) {
	client, server := newTestClient(t)
	ctx := t.Context()
	count, err := client.ZAdd(ctx, "sorted", []ScoredMember{
		{Member: "a", Score: 1}, {Member: "b", Score: 2}, {Member: "c", Score: 3}, {Member: "d", Score: 4},
	}, time.Second)
	if err != nil || count != 4 || server.TTL("sorted") != time.Second {
		t.Fatalf("add=%d, error=%v", count, err)
	}
	count, err = client.ZCard(ctx, "sorted", 2*time.Second)
	if err != nil || count != 4 || server.TTL("sorted") != 2*time.Second {
		t.Fatalf("card=%d, error=%v", count, err)
	}
	members, err := client.ZRange(ctx, "sorted", 0, 1, false, true, 3*time.Second)
	want := []ScoredMember{{Member: "a", Score: 1}, {Member: "b", Score: 2}}
	if err != nil || !reflect.DeepEqual(members, want) || server.TTL("sorted") != 3*time.Second {
		t.Fatalf("range=%v, error=%v", members, err)
	}
	total, members, err := client.ZPage(ctx, "sorted", 2, 2, true, true, 4*time.Second)
	want = []ScoredMember{{Member: "b", Score: 2}, {Member: "a", Score: 1}}
	if err != nil || total != 4 || !reflect.DeepEqual(members, want) || server.TTL("sorted") != 4*time.Second {
		t.Fatalf("page total=%d, members=%v, error=%v", total, members, err)
	}
	_, members, err = client.ZPage(ctx, "sorted", 1, 1, false, false)
	if err != nil || !reflect.DeepEqual(members, []ScoredMember{{Member: "a", Score: 0}}) {
		t.Fatalf("unscored page=%v, error=%v", members, err)
	}
	count, err = client.ZCount(ctx, "sorted", "(1", "+inf", 5*time.Second)
	if err != nil || count != 3 || server.TTL("sorted") != 5*time.Second {
		t.Fatalf("count=%d, error=%v", count, err)
	}
	// 同一边界在有 TTL 和无 TTL 两条路径上使用相同的降序语义。
	for _, ttl := range []time.Duration{0, 6 * time.Second} {
		members, err = client.ZRangeByScore(ctx, "sorted", "1", "3", true, true, []int{1, 2}, ttl)
		want = []ScoredMember{{Member: "b", Score: 2}, {Member: "a", Score: 1}}
		if err != nil || !reflect.DeepEqual(members, want) {
			t.Fatalf("descending range ttl=%v, members=%v, error=%v", ttl, members, err)
		}
	}
	rank, err := client.ZRank(ctx, "sorted", "b", true, 7*time.Second)
	if err != nil || rank != 2 || server.TTL("sorted") != 7*time.Second {
		t.Fatalf("rank=%d, error=%v", rank, err)
	}
	score, err := client.ZIncrBy(ctx, "sorted", 0.5, "b", 8*time.Second)
	if err != nil || score != 2.5 || server.TTL("sorted") != 8*time.Second {
		t.Fatalf("increment score=%v, error=%v", score, err)
	}
	score, err = client.ZScore(ctx, "sorted", "b", 9*time.Second)
	if err != nil || score != 2.5 || server.TTL("sorted") != 9*time.Second {
		t.Fatalf("score=%v, error=%v", score, err)
	}
	_, err = client.ZScore(ctx, "sorted", "missing", time.Second)
	if !errors.Is(err, redis.Nil) {
		t.Fatalf("missing score error=%v", err)
	}
	_, err = client.ZRank(ctx, "sorted", "missing", false, time.Second)
	if !errors.Is(err, redis.Nil) {
		t.Fatalf("missing rank error=%v", err)
	}
	count, err = client.ZRem(ctx, "sorted", []any{"a"}, time.Second)
	if err != nil || count != 1 {
		t.Fatalf("remove=%d, error=%v", count, err)
	}
	count, err = client.ZRemRangeByRank(ctx, "sorted", 0, 0, time.Second)
	if err != nil || count != 1 {
		t.Fatalf("remove rank=%d, error=%v", count, err)
	}
	count, err = client.ZRemRangeByScore(ctx, "sorted", "3", "4", time.Second)
	if err != nil || count != 2 || server.Exists("sorted") {
		t.Fatalf("remove score=%d, error=%v", count, err)
	}
}

func TestScoredResultsProtocolShapes(t *testing.T) {
	want := []ScoredMember{{Member: "a", Score: 1.25}, {Member: "b", Score: 2.5}}
	for _, reply := range []any{
		[]any{"a", "1.25", "b", "2.5"},
		[]any{"a", 1.25, "b", 2.5},
		[]any{[]any{"a", 1.25}, []any{"b", 2.5}},
	} {
		members, err := scoredResults(reply, true)
		if err != nil || !reflect.DeepEqual(members, want) {
			t.Fatalf("reply=%#v, members=%v, error=%v", reply, members, err)
		}
	}
}
