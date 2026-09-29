package main

import (
	"context"
	"database/sql"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/golang-jwt/jwt/v5"
	_ "github.com/lib/pq"
	"github.com/stellar/go/keypair"
)

const (
	userID       = "10000000-0000-0000-0000-000000000001"
	circleID     = "20000000-0000-0000-0000-000000000001"
	contribution = "30000000-0000-0000-0000-000000000001"
	wallet       = "GAX23V3WWDPPR5WRER3KTEUTDLSCGZYMSJY5FDRRKKCIQ4JADF5T27RC"
)

func main() {
	databaseURL := flag.String("database-url", "", "Postgres connection URL")
	privateKeyFile := flag.String("private-key", "", "RSA private key PEM")
	stellarKeys := flag.Bool("stellar-keys", false, "print a random Stellar secret and public key")
	flag.Parse()
	if *stellarKeys {
		kp, err := keypair.Random()
		if err != nil {
			panic(err)
		}
		fmt.Printf("%s\n%s\n", kp.Seed(), kp.Address())
		return
	}
	if *databaseURL == "" || *privateKeyFile == "" {
		panic("database-url and private-key are required")
	}

	ctx := context.Background()
	db, err := sql.Open("postgres", *databaseURL)
	if err != nil {
		panic(err)
	}
	defer db.Close()

	statements := []string{
		`INSERT INTO users (id, wallet_address, email, display_name) VALUES ($1,$2,'perf@moistello.test','Performance User') ON CONFLICT (id) DO NOTHING`,
		`INSERT INTO circles (id,name,description,contribution_amount,max_members,organizer_id,status) VALUES ($1,'Performance Circle','seeded for k6',10,10,$2,'active') ON CONFLICT (id) DO NOTHING`,
		`INSERT INTO contributions (id,circle_id,user_id,round_number,amount,status) VALUES ($1,$2,$3,1,10,'confirmed') ON CONFLICT (id) DO NOTHING`,
	}
	args := [][]any{{userID, wallet}, {circleID, userID}, {contribution, circleID, userID}}
	for i, statement := range statements {
		if _, err := db.ExecContext(ctx, statement, args[i]...); err != nil {
			panic(err)
		}
	}

	pemBytes, err := os.ReadFile(*privateKeyFile)
	if err != nil {
		panic(err)
	}
	key, err := jwt.ParseRSAPrivateKeyFromPEM(pemBytes)
	if err != nil {
		panic(err)
	}
	now := time.Now().UTC()
	token := jwt.NewWithClaims(jwt.SigningMethodRS256, jwt.MapClaims{
		"sub": userID, "wallet": wallet, "role": "user",
		"iat": now.Unix(), "exp": now.Add(time.Hour).Unix(),
	})
	token.Header["kid"] = "perf"
	signed, err := token.SignedString(key)
	if err != nil {
		panic(err)
	}
	fmt.Print(signed)
}
