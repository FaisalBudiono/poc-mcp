package todos

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
)

type Todo struct {
	ID   int64  `json:"id"`
	Text string `json:"text"`
	Done bool   `json:"done"`
}

type Core struct {
	mu sync.RWMutex
}

func New() *Core {
	return &Core{}
}

func newTodo(id int64, text string) Todo {
	return Todo{
		ID:   id,
		Text: text,
	}
}

var (
	ErrNotFound  = errors.New("todo not found")
	ErrEmptyText = errors.New("empty text")
)

func (c *Core) List() ([]Todo, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	return readFiles()
}

func (c *Core) Add(text string) (Todo, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if text == "" {
		return Todo{}, ErrEmptyText
	}

	list, err := readFiles()
	if err != nil {
		return Todo{}, err
	}

	var maxID int64
	for _, t := range list {
		if t.ID > maxID {
			maxID = t.ID
		}
	}

	id := maxID + 1
	t := newTodo(id, text)
	todos := append(list, t)

	err = syncFiles(todos)
	if err != nil {
		return Todo{}, err
	}

	return t, nil
}

func (c *Core) Find(id int64) (Todo, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	list, err := readFiles()
	if err != nil {
		return Todo{}, err
	}

	for _, todo := range list {
		if todo.ID == id {
			return todo, nil
		}
	}
	return Todo{}, ErrNotFound
}

func (c *Core) Remove(id int64) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	list, err := readFiles()
	if err != nil {
		return err
	}

	removedList, err := func(todos []Todo, removedID int64) ([]Todo, error) {
		for i, todo := range todos {
			if todo.ID == id {
				todos = append(todos[:i], todos[i+1:]...)
				return todos, nil
			}
		}

		return nil, ErrNotFound
	}(list, id)
	if err != nil {
		return err
	}

	return syncFiles(removedList)
}

func (c *Core) ToggleDone(id int64) (Todo, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	list, err := readFiles()
	if err != nil {
		return Todo{}, err
	}

	uTodo, uList, err := func(todos []Todo, updatedID int64) (
		Todo, []Todo, error,
	) {
		for i, todo := range todos {
			if todo.ID == id {
				todo.Done = !todo.Done
				todos[i] = todo
				return todo, todos, nil
			}
		}

		return Todo{}, nil, ErrNotFound
	}(list, id)
	if err != nil {
		return Todo{}, err
	}

	err = syncFiles(uList)
	if err != nil {
		return Todo{}, err
	}

	return uTodo, nil
}

var (
	dir = "./temp"
	fp  = filepath.Join(dir, "data.json")
)

func readFiles() ([]Todo, error) {
	f, err := os.OpenFile(fp, os.O_CREATE|os.O_RDONLY, 0o644)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var res []Todo
	err = json.NewDecoder(f).Decode(&res)
	if err != nil {
		return nil, err
	}

	return res, nil
}

func syncFiles(todos []Todo) error {
	f, err := os.OpenFile(fp, os.O_TRUNC|os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()

	err = json.NewEncoder(f).Encode(todos)
	if err != nil {
		return err
	}

	return nil
}
