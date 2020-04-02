package dao

import "github.com/go-redis/redis"

type IRedisDao interface {
    AddConnect(addr string) error
    DelConnect(addr string) error
    GetConnects() ([]string, error)
    GetConnectNum() (int64, error)
}

var RedisDao IRedisDao = &RedisNilClient{}

type RedisNilClient struct {
}

func (r *RedisNilClient) AddConnect(addr string) error {
    return nil
}

func (r *RedisNilClient) DelConnect(addr string) error {
    return nil
}

func (r *RedisNilClient) GetConnects() ([]string, error) {
    return nil, nil
}

func (r *RedisNilClient) GetConnectNum() (int64, error) {
    return 0, nil
}

type RedisClient redis.Client

func NewRedisDao(addr, password string) *RedisClient {
    rc := redis.NewClient(&redis.Options{
        Addr:     addr,
        Password: password,
    })
    return (*RedisClient)(rc)
}

// 存储当前所有连接
func genConnectsKey() string {
    return "hole|connects"
}

func (rc *RedisClient) AddConnect(addr string) error {
    key := genConnectsKey()
    return rc.SAdd(key, addr).Err()
}

func (rc *RedisClient) DelConnect(addr string) error {
    key := genConnectsKey()
    return rc.SRem(key, addr).Err()
}

func (rc *RedisClient) GetConnects() ([]string, error) {
    key := genConnectsKey()
    return rc.SMembers(key).Result()
}

func (rc *RedisClient) GetConnectNum() (int64, error) {
    key := genConnectsKey()
    return rc.SCard(key).Result()
}
