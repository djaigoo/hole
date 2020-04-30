package confs

import "github.com/BurntSushi/toml"

type Conf struct {
    LocalPort    int    `toml:"local_port"`
    LocalCrtFile string `toml:"local_crt_file"`
    LocalKeyFile string `toml:"local_key_file"`
    
    Server        string `toml:"server"`
    ServerPort    int    `toml:"server_port"`
    ServerCrtFile string `toml:"server_crt_file"`
    ServerKeyFile string `toml:"server_key_file"`
    
    Admin         bool   `toml:"admin"`
    AdminPort     int    `toml:"admin_port"`
    RedisAddr     string `toml:"redis_addr"`
    RedisPassword string `toml:"redis_password"`
    Pprof         bool   `toml:"pprof"`
    
    Debug bool `toml:"debug"`
}

func ReadConfigFile(path string) (*Conf, error) {
    c := &Conf{}
    _, err := toml.DecodeFile(path, c)
    return c, err
}
