package userservice

type UserServiceRPC interface {
	CreateNewUser(name string, age int)
	GetUsers() []User
}

type UserService struct {
	users map[string]User
}

type User struct {
	Name string
	Age  int
}

func (u *UserService) CreateNewUser(name string, age int) {
	if u.users == nil {
		u.users = make(map[string]User)
	}
	u.users[name] = User{
		Name: name,
		Age:  age,
	}
}

func (u *UserService) GetUsers() []User {
	if u.users == nil {
		return []User{}
	}
	var users []User
	for _, user := range u.users {
		users = append(users, user)
	}
	return users
}
