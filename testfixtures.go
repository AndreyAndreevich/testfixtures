package testfixtures

import (
	"database/sql"
	"fmt"
	"io/ioutil"
	"path"
	"path/filepath"
	"reflect"
	"strings"

	"gopkg.in/yaml.v2"
)

type Context struct {
	db            *sql.DB
	helper        Helper
	fixturesFiles []*fixtureFile
}

func NewFolder(db *sql.DB, helper Helper, folderName string) (*Context, error) {
	fixtures, err := fixturesFromFolder(folderName)
	if err != nil {
		return nil, err
	}

	return &Context{
		db:            db,
		helper:        helper,
		fixturesFiles: fixtures,
	}, nil
}

func NewFiles(db *sql.DB, helper Helper, fileNames ...string) (*Context, error) {
	fixtures, err := fixturesFromFiles(fileNames...)
	if err != nil {
		return nil, err
	}

	return &Context{
		db:            db,
		helper:        helper,
		fixturesFiles: fixtures,
	}, nil
}

func (c *Context) Load() error {
	if !skipDatabaseNameCheck {
		if !dbnameRegexp.MatchString(c.helper.databaseName(c.db)) {
			return errNotTestDatabase
		}
	}

	err := c.helper.disableReferentialIntegrity(c.db, func(tx *sql.Tx) error {
		for _, file := range c.fixturesFiles {
			err := file.delete(tx, c.helper)
			if err != nil {
				return err
			}

			err = c.helper.whileInsertOnTable(tx, file.fileNameWithoutExtension(), func() error {
				return file.insert(tx, c.helper)
			})
			if err != nil {
				return err
			}
		}
		return nil
	})
	return err
}

type fixtureFile struct {
	path     string
	fileName string
	content  []byte
}

var (
	// ErrWrongCastNotAMap is returned when a map is not a map[interface{}]interface{}
	ErrWrongCastNotAMap = fmt.Errorf("Could not cast record: not a map[interface{}]interface{}")

	// ErrFileIsNotSliceOrMap is returned the the fixture file is not a slice or map.
	ErrFileIsNotSliceOrMap = fmt.Errorf("The fixture file is not a slice or map")

	// ErrKeyIsNotString is returned when a record is not of type string
	ErrKeyIsNotString = fmt.Errorf("Record map key is not string")
)

func (f *fixtureFile) fileNameWithoutExtension() string {
	return strings.Replace(f.fileName, filepath.Ext(f.fileName), "", 1)
}

func (f *fixtureFile) delete(tx *sql.Tx, h Helper) error {
	_, err := tx.Exec(fmt.Sprintf("DELETE FROM %s", h.quoteKeyword(f.fileNameWithoutExtension())))
	return err
}

func (f *fixtureFile) buildInsertSQL(h Helper, record map[interface{}]interface{}) (sqlStr string, values []interface{}, err error) {
	var sqlColumns string
	var sqlValues string
	i := 1
	for key, value := range record {
		if len(sqlColumns) > 0 {
			sqlColumns += ", "
			sqlValues += ", "
		}
		keyStr, ok := key.(string)
		if !ok {
			err = ErrKeyIsNotString
			return
		}
		sqlColumns += h.quoteKeyword(keyStr)
		switch h.paramType() {
		case paramTypeDollar:
			sqlValues += fmt.Sprintf("$%d", i)
		case paramTypeQuestion:
			sqlValues += "?"
		case paramTypeColon:
			if isDateTime(value) {
				sqlValues += fmt.Sprintf("to_date(:%d, 'YYYY-MM-DD HH24:MI:SS')", i)
			} else if isDate(value) {
				sqlValues += fmt.Sprintf("to_date(:%d, 'YYYY-MM-DD')", i)
			} else if isTime(value) {
				sqlValues += fmt.Sprintf("to_date(:%d, 'HH24:MI:SS')", i)
			} else {
				sqlValues += fmt.Sprintf(":%d", i)
			}
		}
		i++
		values = append(values, value)
	}

	sqlStr = fmt.Sprintf("INSERT INTO %s (%s) VALUES (%s)", h.quoteKeyword(f.fileNameWithoutExtension()), sqlColumns, sqlValues)
	return
}

func (f *fixtureFile) insert(tx *sql.Tx, h Helper) error {
	var rows interface{}
	err := yaml.Unmarshal(f.content, &rows)
	if err != nil {
		return err
	}

	t := reflect.TypeOf(rows)
	v := reflect.ValueOf(rows)
	switch t.Kind() {
	case reflect.Slice:
		length := v.Len()
		for i := 0; i < length; i++ {
			record, ok := v.Index(i).Interface().(map[interface{}]interface{})
			if !ok {
				return ErrWrongCastNotAMap
			}

			sqlStr, values, err := f.buildInsertSQL(h, record)
			if err != nil {
				return err
			}
			_, err = tx.Exec(sqlStr, values...)
			if err != nil {
				return err
			}
		}
	case reflect.Map:
		keys := v.MapKeys()
		for _, key := range keys {
			record, ok := v.MapIndex(key).Interface().(map[interface{}]interface{})
			if !ok {
				return ErrWrongCastNotAMap
			}

			sqlStr, values, err := f.buildInsertSQL(h, record)
			if err != nil {
				return err
			}
			_, err = tx.Exec(sqlStr, values...)
			if err != nil {
				return err
			}
		}
	default:
		return ErrFileIsNotSliceOrMap
	}
	return nil
}

func fixturesFromFolder(folderName string) ([]*fixtureFile, error) {
	var files []*fixtureFile
	fileinfos, err := ioutil.ReadDir(folderName)
	if err != nil {
		return nil, err
	}

	for _, fileinfo := range fileinfos {
		if !fileinfo.IsDir() && filepath.Ext(fileinfo.Name()) == ".yml" {
			fixture := &fixtureFile{
				path:     path.Join(folderName, fileinfo.Name()),
				fileName: fileinfo.Name(),
			}
			fixture.content, err = ioutil.ReadFile(fixture.path)
			if err != nil {
				return nil, err
			}
			files = append(files, fixture)
		}
	}
	return files, nil
}

func fixturesFromFiles(fileNames ...string) ([]*fixtureFile, error) {
	var (
		fixtureFiles []*fixtureFile
		err          error
	)

	for _, f := range fileNames {
		fixture := &fixtureFile{
			path:     f,
			fileName: filepath.Base(f),
		}
		fixture.content, err = ioutil.ReadFile(fixture.path)
		if err != nil {
			return nil, err
		}
		fixtureFiles = append(fixtureFiles, fixture)
	}

	return fixtureFiles, nil
}
