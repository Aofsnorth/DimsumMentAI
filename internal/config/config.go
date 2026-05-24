package config

type Config struct {
	Server ServerConfig `yaml:"server"`
	Bot    BotConfig    `yaml:"bot"`
	Skin   SkinConfig   `yaml:"skin"`
}

type ServerConfig struct {
	Address string `yaml:"address"`
	Offline bool   `yaml:"offline"`
}

type BotConfig struct {
	Name string `yaml:"name"`
}

type SkinConfig struct {
	ImagePath    string `yaml:"image_path"`
	GeometryPath string `yaml:"geometry_path"`
	GeometryName string `yaml:"geometry_name"`
	ArmSize      string `yaml:"arm_size"`
}
