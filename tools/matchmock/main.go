package main

import (
	"flag"
	"fmt"
	"log"
	"net/http"

	"github.com/99designs/gqlgen/graphql/playground"

	"rctHubBackend/internal/graphql"
	"rctHubBackend/internal/matchfixture"
	"rctHubBackend/pkg/jwtutil"
)

func main() {
	address := flag.String("addr", "127.0.0.1:8091", "listen address")
	flag.Parse()
	reader, err := matchfixture.NewReader()
	if err != nil {
		log.Fatal(err)
	}
	executor := matchfixture.NewExecutor(reader)
	resolver := graphql.NewResolver(nil, executor).
		WithFormalMatchReader(reader).
		WithBeatmapReader(reader).
		WithPrivateReaders(reader.PrivateUsers(), reader.PrivateRooms())
	server := graphql.NewHandler(resolver)
	http.Handle("/graphql", http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		ctx := graphql.WithClaims(request.Context(), &jwtutil.Claims{OsuID: 1001, Username: "fixture-user"})
		server.ServeHTTP(writer, request.WithContext(ctx))
	}))
	http.Handle("/", playground.Handler("RCTS1 Match Mock", "/graphql"))
	fmt.Printf("RCTS1 Match mock listening at http://%s\n", *address)
	log.Fatal(http.ListenAndServe(*address, nil))
}
