package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sort"
	"strconv"

	"github.com/sirupsen/logrus"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

const (
	UserTypeUsual   uint = 0
	UserTypeAdmin   uint = 1
	UserTypeLimited uint = 2
)

type CardFields struct {
	Name        string `form:"name" binding:"required"`
	Company     string `form:"company"`
	Position    string `form:"position"`
	Description string `form:"description"`
	Phone       string `form:"phone"`
	Email       string `form:"email" binding:"omitempty,email"`
	Telegram    string `form:"telegram"`
	Whatsapp    string `form:"whatsapp"`
	VK          string `form:"vk"`
	IsHidden    bool   `form:"hidden"`
}

type Card struct {
	ID     uint `gorm:"primaryKey"`
	Owner  uint
	Fields CardFields `gorm:"embedded"`
	Avatar string
	Logo   string
}

type User struct {
	ID         uint `gorm:"primaryKey"`
	ProviderID string
	Name       string
	Type       uint
}

type Database interface {
	SignUser(pid, name string) (string, error)
	GetUser(user *User) error
	DeleteUser(id uint) error
	CreateCard(owner uint, fields CardFields) (Card, error)
	UpdateCard(card Card) error
	GetCard(id uint) (Card, error)
	DeleteCard(id uint) error
	ListCards(uid uint) ([]Card, error)
	ListUsers() ([]User, error)
}

type ByID []Card

func (a ByID) Len() int           { return len(a) }
func (a ByID) Swap(i, j int)      { a[i], a[j] = a[j], a[i] }
func (a ByID) Less(i, j int) bool { return a[i].ID < a[j].ID }

type PGDB struct {
	DB              *gorm.DB
	Storage         *BlobStorage
	DefaultUserType uint
}

func (db *PGDB) SignUser(pid, name string) (string, error) {
	var user User

	result := db.DB.Where("provider_id = ?", pid).First(&user)
	if result.Error != nil {
		if errors.Is(result.Error, gorm.ErrRecordNotFound) {
			user = User{ProviderID: pid, Name: name, Type: db.DefaultUserType}
			result = db.DB.Create(&user)
		}
	}

	if result.Error != nil {
		return "", result.Error
	}

	return strconv.FormatUint(uint64(user.ID), 10), nil
}

func (db *PGDB) GetUser(user *User) error {
	result := db.DB.First(user)
	return result.Error
}

func (db *PGDB) DeleteUser(id uint) error {
	user := User{ID: uint(id)}

	result := db.DB.Delete(&user)
	if result.Error != nil {
		return result.Error
	}

	cards, err := db.ListCards(id)
	if err != nil {
		return err
	}
	for _, card := range cards {
		e := db.DeleteCard(card.ID)
		if e != nil {
			err = e
		}
	}
	return err
}

func (db *PGDB) CreateCard(owner uint, fields CardFields) (Card, error) {
	card := Card{Owner: uint(owner), Fields: fields}
	result := db.DB.Create(&card)
	if result.Error != nil {
		return card, result.Error
	}
	return card, nil
}

func (db *PGDB) UpdateCard(card Card) error {
	result := db.DB.Save(&card)
	return result.Error
}

func (db *PGDB) GetCard(id uint) (Card, error) {
	card := Card{ID: uint(id)}
	result := db.DB.First(&card)
	return card, result.Error
}

func (db *PGDB) DeleteCard(id uint) error {
	card := Card{ID: uint(id)}
	result := db.DB.Delete(&card)

	db.Storage.DelKey(context.Background(), card.Avatar)
	db.Storage.DelKey(context.Background(), card.Logo)

	return result.Error
}

func (db *PGDB) ListCards(uid uint) ([]Card, error) {
	cards := []Card{}

	result := db.DB.Where(&Card{Owner: uid}).Find(&cards)
	sort.Sort(ByID(cards))
	return cards, result.Error
}

func (db *PGDB) ListUsers() ([]User, error) {
	users := []User{}

	result := db.DB.Find(&users)
	return users, result.Error
}

func SetupDB(store *BlobStorage, log *logrus.Logger) Database {
	host := os.Getenv("PG_HOST")
	port := os.Getenv("PG_PORT")
	user := os.Getenv("PG_USER")
	password := os.Getenv("PG_PASSWORD")
	dbname := os.Getenv("PG_NAME")
	sslmode := os.Getenv("PG_SSLMODE")
	timezone := os.Getenv("PG_TIMEZONE")

	if timezone != "" {
		timezone = "TimeZone=" + timezone
	}

	dsn := fmt.Sprintf(
		"host=%s port=%s user=%s password=%s dbname=%s sslmode=%s %s",
		host, port, user, password, dbname, sslmode, timezone,
	)

	dut_str := os.Getenv("DEFAULT_USER_TYPE")
	var dut uint = UserTypeLimited
	if dut_str != "" {
		d, err := strconv.Atoi(dut_str)
		dut = uint(d)
		if err != nil {
			log.Fatalf("Failed to parse DEFAULT_USER_TYPE: %s", dut_str)
		}
	}

	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{
		Logger: gormlog{log},
	})

	if err != nil {
		log.WithFields(logrus.Fields{
			"err": err,
		}).Fatal("Failed to setup DB client")
	}

	err = db.AutoMigrate(&User{})
	if err != nil {
		log.WithFields(logrus.Fields{
			"err": err,
		}).Fatal("Failed to setup DB client")
	}
	err = db.AutoMigrate(&Card{})
	if err != nil {
		log.WithFields(logrus.Fields{
			"err": err,
		}).Fatal("Failed to setup DB client")
	}

	return &PGDB{DB: db, Storage: store, DefaultUserType: dut}
}
