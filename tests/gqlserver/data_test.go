package gqlserver

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"sync"
	"testing"

	"github.com/Khan/genqlient/graphql"
)

var clientFactories = map[string]func(*testing.T) graphql.Client{
	"server": func(t *testing.T) graphql.Client {
		t.Helper()
		server, err := StartServer()
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			if err := server.Stop(); err != nil {
				t.Error(err)
			}
		})
		return graphql.NewClient(server.QueryURL(), http.DefaultClient)
	},
	"import": func(t *testing.T) graphql.Client {
		t.Helper()
		config, err := ImportConfig("Test")
		if err != nil {
			t.Fatal(err)
		}
		return config.Client
	},
}

func request(t *testing.T, client graphql.Client, query string, result any) {
	t.Helper()
	if result == nil {
		result = new(json.RawMessage)
	}
	resp := &graphql.Response{Data: result}
	if err := client.MakeRequest(context.Background(), &graphql.Request{Query: query}, resp); err != nil {
		t.Fatal(err)
	}
	if len(resp.Errors) != 0 {
		t.Fatal(resp.Errors)
	}
}

const dataQuery = `{
	users { id name emails age status }
	posts { id title content createdAt author { id name emails age } }
	serverInfo { totalUsers totalPosts }
}`

func TestIndependentData(t *testing.T) {
	for name, newClient := range clientFactories {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			first, second := newClient(t), newClient(t)
			type testData struct {
				Users      []*User
				Posts      []*Post
				ServerInfo ServerInfo
			}
			var initial testData
			request(t, second, dataQuery, &initial)
			if initial.ServerInfo.TotalUsers != 2 || initial.ServerInfo.TotalPosts != 6 ||
				len(initial.Users) != 2 || len(initial.Posts) != 6 ||
				initial.Users[0].Name != "John Doe" || initial.Users[1].Name != "Jane Smith" {
				t.Fatalf("unexpected initial data: %+v", initial)
			}

			request(t, first, `mutation {
				updateUser(id: "User:1", input: {name: "Changed", email: "changed@example.com", age: 99}) { id }
				deleteUser(id: "User:2")
				createUser(input: {name: "New", email: "new@example.com"}) { id }
			}`, nil)
			var changed struct{ Posts []*Post }
			request(t, first, dataQuery, &changed)
			if changed.Posts[0].Author.Name != "Changed" {
				t.Fatal("updated user not reflected in post author")
			}

			// Both an already-running instance and one created after the mutation
			// must retain the complete original data, including post authors.
			for _, client := range []graphql.Client{second, newClient(t)} {
				var got testData
				request(t, client, dataQuery, &got)
				if !reflect.DeepEqual(got, initial) {
					t.Fatalf("mutation leaked into another instance: got %+v, want %+v", got, initial)
				}
			}
		})
	}
}

func TestResolverSnapshots(t *testing.T) {
	var resolver Resolver // No constructor required.
	ctx := context.Background()
	query, mutation := resolver.Query(), resolver.Mutation()
	users, err := query.Users(ctx)
	if err != nil {
		t.Fatal(err)
	}
	posts, err := query.Posts(ctx, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	beforeUsers, _ := json.Marshal(users)
	beforePosts, _ := json.Marshal(posts)

	name, email, age := "Changed", "changed@example.com", 99
	updated, err := mutation.UpdateUser(ctx, "User:1", UpdateUserInput{Name: &name, Email: &email, Age: &age})
	if err != nil {
		t.Fatal(err)
	}
	age = 100 // The stored age must not alias the caller's input either.
	if *updated.Age != 99 {
		t.Fatal("update retained caller's age pointer")
	}
	if _, err := mutation.DeleteUser(ctx, "User:1"); err != nil {
		t.Fatal(err)
	}
	created, err := mutation.CreateUser(ctx, CreateUserInput{Name: "New", Age: &age})
	if err != nil {
		t.Fatal(err)
	}
	if created.ID != "User:3" {
		t.Fatalf("ID reused after deletion: %s", created.ID)
	}
	age = 101
	if *created.Age != 100 {
		t.Fatal("create retained caller's age pointer")
	}

	// Objects and slice backing arrays previously handed to gqlgen must stay
	// untouched even after update, deletion, and creation have all completed.
	afterUsers, _ := json.Marshal(users)
	afterPosts, _ := json.Marshal(posts)
	if string(beforeUsers) != string(afterUsers) || string(beforePosts) != string(afterPosts) {
		t.Fatal("mutation changed a previously returned snapshot")
	}
	currentPosts, err := query.Posts(ctx, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if currentPosts[0].Author.Name != name {
		t.Fatal("post authors did not follow the updated user")
	}
}

// Exercise serialization, not just resolver calls: gqlgen accesses the returned
// objects and slices outside resolver locks, including through interfaces,
// unions, profiles, and nested author/manager fields.
func TestConcurrentRequests(t *testing.T) {
	const query = `{
		users { id name emails age manager { name } posts { posts { author { name emails age } } } }
		user(id: "User:1") { name }
		maybeUser(id: "User:1") { name }
		primaryUser { name } secondaryUser { name }
		userName(user: "User:1") userNames(users: ["User:1", "User:2"])
		nodeLabel(node: "User:1") favoriteUserID favoriteUserIDs favoriteNodeID
		serverInfo { totalUsers totalPosts }
		posts { id author { name emails age } }
		userProfile(includeStats: true) { user { name } postCount }
		postTitles
		node(id: "User:1") { ...UserFields }
		nodes { ...UserFields ...PostFields }
		favoriteNode { ...PostFields }
		featured { ...UserFields }
		timestamped { ...PostFields }
		search(query: "") { ...UserFields ...PostFields }
		searchConnection(query: "") { edges { node { ...UserFields ...PostFields } } }
		usersByStatus(status: ACTIVE) { name emails age }
		sortedUsers(sort: {field: NAME, direction: DESC}) { name emails age }
	}
	fragment UserFields on User { id name emails age }
	fragment PostFields on Post { id author { name emails age } }`
	for name, newClient := range clientFactories {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			client := newClient(t)
			var wg sync.WaitGroup
			start := make(chan struct{})
			for worker := range 8 {
				wg.Add(1)
				go func() {
					defer wg.Done()
					<-start
					for iteration := range 20 {
						if worker%2 == 0 {
							request(t, client, query, nil)
							continue
						}
						var result struct{ CreateUser User }
						request(t, client, `mutation { createUser(input: {name: "New", email: "new@example.com"}) { id } }`, &result)
						request(t, client, fmt.Sprintf(`mutation {
							updateUser(id: "User:1", input: {name: "Worker %d", email: "worker%d@example.com", age: %d}) { name emails age }
							touchNode(id: "User:1") { ... on User { name emails age } }
							touchResult(id: "Post:1") { ... on Post { author { name emails age } } }
							deleteUser(id: %q)
						}`, worker, worker, iteration, result.CreateUser.ID), nil)
					}
				}()
			}
			close(start)
			wg.Wait()
			var result struct{ ServerInfo ServerInfo }
			request(t, client, `{ serverInfo { totalUsers totalPosts } }`, &result)
			if result.ServerInfo.TotalUsers != 2 || result.ServerInfo.TotalPosts != 6 {
				t.Fatalf("lost concurrent mutation: %+v", result.ServerInfo)
			}
		})
	}
}
