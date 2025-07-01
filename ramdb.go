package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"slices"
	"strconv"
	"sync"

	"github.com/sirupsen/logrus"
)

// O(n)
func remove[T comparable](l []T, item T) []T {
	if len(l) == 0 {
		return l
	}
	out := make([]T, 0)
	for _, element := range l {
		if element != item {
			out = append(out, element)
		}
	}
	return out
}

type RamDB struct {
	Users           map[uint]User // User ID -> User
	Cards           map[uint]Card // Card ID -> Card
	MaxUID          uint
	MaxCID          uint
	usersByProvider map[string]uint // Provider ID -> User ID
	cardsByUser     map[uint][]uint // User ID -> Slice of Card ID's
	storage         *BlobStorage
	ctx             context.Context
	name            string
	mu              sync.Mutex
	defaultUserType uint
	admins          []string
}

func LoadRamDb(
	ctx context.Context,
	log *logrus.Logger,
	storage *BlobStorage,
	name string,
	defaultUserType uint,
	admins []string,
) (Database, error) {
	db := RamDB{
		storage:         storage,
		ctx:             ctx,
		name:            name,
		defaultUserType: defaultUserType,
		admins:          admins,
	}
	_, obj, err := storage.GetKey(ctx, name, false)
	if err != nil {
		log.Warnf("There is no DB %s; Creating one", name)
		db.init()
		return &db, db.save()
	}
	data, err := io.ReadAll(obj)
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(data, &db); err != nil {
		return nil, err
	}
	db.init()
	return &db, nil
}

func (db *RamDB) init() {
	if db.Users == nil {
		db.Users = make(map[uint]User)
	}
	if db.Cards == nil {
		db.Cards = make(map[uint]Card)
	}
	if db.usersByProvider == nil {
		db.usersByProvider = make(map[string]uint)
	}
	if db.cardsByUser == nil {
		db.cardsByUser = make(map[uint][]uint)
	}
	for _, user := range db.Users {
		db.usersByProvider[user.ProviderID] = user.ID
	}
	for _, card := range db.Cards {
		db.cardsByUser[card.Owner] = append(db.cardsByUser[card.Owner], card.ID)
	}
}

func (db *RamDB) save() error {
	data, err := json.Marshal(db)
	if err != nil {
		return err
	}
	err = db.storage.WriteKey(db.ctx, db.name, bytes.NewReader(data), int64(len(data)), false)
	return err
}

func (db *RamDB) SignUser(pid, name string) (string, error) {
	db.mu.Lock()
	defer db.mu.Unlock()
	uid, ok := db.usersByProvider[pid]
	if !ok {
		typ := db.defaultUserType
		if slices.Contains(db.admins, pid) {
			typ = UserTypeAdmin
		}
		user := User{
			ID:         db.MaxUID + 1,
			ProviderID: pid,
			Name:       name,
			Type:       typ,
		}
		uid = user.ID
		db.MaxUID = user.ID
		db.Users[user.ID] = user
		db.usersByProvider[pid] = user.ID
		db.cardsByUser[user.ID] = []uint{}
		if err := db.save(); err != nil {
			return "", err
		}
	}
	return strconv.FormatUint(uint64(uid), 10), nil
}

func (db *RamDB) GetUser(user *User) error {
	found, ok := db.Users[user.ID]
	if !ok {
		return fmt.Errorf("User %d not found", user.ID)
	}
	*user = found
	return nil
}

func (db *RamDB) DeleteUser(uid uint) error {
	user, ok := db.Users[uid]
	if !ok {
		return nil
	}
	cards, err := db.ListCards(uid)
	if err != nil {
		return err
	}
	for _, card := range cards {
		e := db.DeleteCard(card.ID)
		if e != nil {
			err = e
		}
	}
	delete(db.Users, user.ID)
	delete(db.usersByProvider, user.ProviderID)
	return db.save()
}

func (db *RamDB) CreateCard(owner uint, fields CardFields) (Card, error) {
	card := Card{
		ID:     db.MaxCID + 1,
		Owner:  uint(owner),
		Fields: fields,
	}
	db.MaxCID = card.ID
	db.Cards[card.ID] = card
	db.cardsByUser[owner] = append(db.cardsByUser[owner], card.ID)
	return card, db.save()
}

func (db *RamDB) UpdateCard(card Card) error {
	db.Cards[card.ID] = card
	return db.save()
}

func (db *RamDB) UpdateUser(user User) error {
	db.Users[user.ID] = user
	return db.save()
}

func (db *RamDB) GetCard(cid uint) (Card, error) {
	card, ok := db.Cards[cid]
	if !ok {
		return card, fmt.Errorf("Card %d not found", cid)
	}
	return card, nil
}

func (db *RamDB) DeleteCard(cid uint) error {
	card, ok := db.Cards[cid]
	if ok {
		usercards, ok := db.cardsByUser[card.Owner]
		if ok {
			// O(n)
			db.cardsByUser[card.Owner] = remove(usercards, card.ID)
		}
	}
	delete(db.Cards, cid)
	return db.save()
}

func (db *RamDB) ListCards(uid uint) ([]Card, error) {
	result := []Card{}
	cards, ok := db.cardsByUser[uid]
	if ok {
		for _, cid := range cards {
			card, ok := db.Cards[cid]
			if ok {
				result = append(result, card)
			}
		}
	}
	return result, nil
}

func (db *RamDB) ListUsers() ([]User, error) {
	result := []User{}
	for _, user := range db.Users {
		result = append(result, user)
	}
	return result, nil
}
