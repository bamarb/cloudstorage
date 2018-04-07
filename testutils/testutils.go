package testutils

import (
	"bufio"
	"bytes"
	"context"
	"flag"
	"fmt"
	"io/ioutil"
	"log"
	"os"
	"sort"
	"sync"
	"time"

	"github.com/araddon/gou"
	"github.com/stretchr/testify/assert"
	"google.golang.org/api/iterator"

	"github.com/lytics/cloudstorage"
)

var (
	verbose   *bool
	setupOnce = sync.Once{}
)

func init() {
	Setup()
}

type TestingT interface {
	Logf(format string, args ...interface{})
	Fatalf(format string, args ...interface{})
	Errorf(format string, args ...interface{})
}

// Setup enables -vv verbose logging or sends logs to /dev/null
// env var VERBOSELOGS=true was added to support verbose logging with alltests
func Setup() {
	setupOnce.Do(func() {

		if flag.CommandLine.Lookup("vv") == nil {
			verbose = flag.Bool("vv", false, "Verbose Logging?")
		}

		flag.Parse()
		logger := gou.GetLogger()
		if logger != nil {
			// don't re-setup
		} else {
			if (verbose != nil && *verbose == true) || os.Getenv("VERBOSELOGS") != "" {
				gou.SetupLogging("debug")
				gou.SetColorOutput()
			} else {
				// make sure logging is always non-nil
				dn, _ := os.Open(os.DevNull)
				gou.SetLogger(log.New(dn, "", 0), "error")
			}
		}
	})
}

func Clearstore(t TestingT, store cloudstorage.Store) {
	//t.Logf("----------------Clearstore-----------------\n")
	q := cloudstorage.NewQueryAll()
	q.Sorted()
	ctx := gou.NewContext(context.Background(), "clearstore")
	iter, _ := store.Objects(ctx, q)
	objs, err := cloudstorage.ObjectsAll(iter)
	if err != nil {
		t.Fatalf("Could not list store %v", err)
	}
	for _, o := range objs {
		//t.Logf("clearstore(): deleting %v", o.Name())
		err = store.Delete(ctx, o.Name())
		assert.Equal(t, nil, err)
	}

	switch store.Type() {
	case "gcs":
		// GCS is lazy about deletes...
		fmt.Println("doing GCS delete sleep 15")
		time.Sleep(15 * time.Second)
	}
}

func RunTests(t TestingT, s cloudstorage.Store) {

	t.Logf("running basic rw")
	BasicRW(t, s)
	gou.Debugf("finished basicrw")

	t.Logf("running Append")
	Append(t, s)
	gou.Debugf("finished append")

	t.Logf("running ListObjsAndFolders")
	ListObjsAndFolders(t, s)
	gou.Debugf("finished ListObjsAndFolders")

	t.Logf("running Truncate")
	Truncate(t, s)
	gou.Debugf("finished Truncate")

	t.Logf("running NewObjectWithExisting")
	NewObjectWithExisting(t, s)
	gou.Debugf("finished NewObjectWithExisting")

	t.Logf("running TestReadWriteCloser")
	TestReadWriteCloser(t, s)
	gou.Debugf("finished TestReadWriteCloser")
}

func BasicRW(t TestingT, store cloudstorage.Store) {

	// Ensure the store has a String identifying store type
	assert.NotEqual(t, "", store.String())

	// Read the object from store, delete if it exists
	obj, _ := store.Get(context.Background(), "prefix/test.csv")
	if obj != nil {
		err := obj.Delete()
		assert.Equal(t, nil, err)
	}

	// Create a new object and write to it.
	obj, err := store.NewObject("prefix/test.csv")
	assert.Equal(t, nil, err)
	assert.NotEqual(t, nil, obj)

	// Opening is required for new objects.
	f, err := obj.Open(cloudstorage.ReadWrite)
	assert.Equal(t, nil, err)
	assert.NotEqual(t, nil, f)

	testcsv := "Year,Make,Model\n1997,Ford,E350\n2000,Mercury,Cougar\n"

	w := bufio.NewWriter(f)
	_, err = w.WriteString(testcsv)
	assert.Equal(t, nil, err)
	w.Flush()

	// Close() actually does the upload/flush/write to cloud
	err = obj.Close()
	assert.Equal(t, nil, err)

	// Read the object back out of the cloud store.
	obj2, err := store.Get(context.Background(), "prefix/test.csv")
	assert.Equal(t, nil, err)

	f2, err := obj2.Open(cloudstorage.ReadOnly)
	assert.Equal(t, nil, err)

	bytes, err := ioutil.ReadAll(f2)
	assert.Equal(t, nil, err)

	assert.Equal(t, testcsv, string(bytes))

	// Now delete again
	err = obj2.Delete()
	assert.Equal(t, nil, err)
	obj, err = store.Get(context.Background(), "prefix/test.csv")
	assert.Equal(t, cloudstorage.ErrObjectNotFound, err)
	assert.Equal(t, nil, obj)
}

func Append(t TestingT, store cloudstorage.Store) {

	Clearstore(t, store)

	now := time.Now()

	switch store.Type() {
	case "sftp":
		// the sftp service only has 1 second granularity
		// on timestamps stored
		time.Sleep(time.Millisecond * 1100)
	default:
		time.Sleep(10 * time.Millisecond)
	}

	// Create a new object and write to it.
	obj, err := store.NewObject("append.csv")
	assert.Equal(t, nil, err)

	f1, err := obj.Open(cloudstorage.ReadWrite)
	assert.Equal(t, nil, err)
	assert.NotEqual(t, nil, f1)

	testcsv := "Year,Make,Model\n2003,VW,EuroVan\n2001,Ford,Ranger\n"

	w1 := bufio.NewWriter(f1)
	_, err = w1.WriteString(testcsv)
	assert.Equal(t, nil, err)
	w1.Flush()

	err = obj.Close()
	assert.Equal(t, nil, err)

	// get the object and append to it...
	morerows := "2013,VW,Jetta\n2011,Dodge,Caravan\n"
	obj2, err := store.Get(context.Background(), "append.csv")
	assert.Equal(t, nil, err)

	// snapshot updated time pre-update
	updated := obj2.Updated()
	switch store.Type() {
	case "azure":
		// azure doesn't have sub-second granularity so will always be equal
		assert.True(t, updated.After(now.Add(-time.Second*2)), "updated time was not set")
	default:
		assert.True(t, updated.After(now), "updated time was not set %v vs %v", now, updated)
	}

	time.Sleep(10 * time.Millisecond)

	f2, err := obj2.Open(cloudstorage.ReadWrite)
	assert.Equal(t, nil, err)
	assert.NotEqual(t, nil, f2)

	w2 := bufio.NewWriter(f2)
	ct, err := w2.WriteString(morerows)
	w2.Flush()
	//ct, err := f2.WriteString(morerows)
	assert.Equal(t, nil, err)
	assert.Equal(t, len(morerows), ct)

	switch store.Type() {
	case "s3", "azure", "sftp":
		// azure and s3 have 1 second granularity on LastModified.  wtf.
		time.Sleep(time.Millisecond * 1000)
	}
	//u.Infof("about to call close on the appended file f p = %p", f2)
	f2.Sync()

	err = obj2.Close()
	assert.Equal(t, nil, err)

	// Read the object back out of the cloud storage.
	obj3, err := store.Get(context.Background(), "append.csv")
	assert.Equal(t, nil, err)
	updated3 := obj3.Updated()
	assert.True(t, updated3.After(updated), "updated wrong:  pre=%v post=%v", updated, updated3)
	f3, err := obj3.Open(cloudstorage.ReadOnly)
	assert.Equal(t, nil, err)

	bytes, err := ioutil.ReadAll(f3)
	assert.Equal(t, nil, err)

	assert.Equal(t, testcsv+morerows, string(bytes), "not the rows we expected.")
}

func dumpfile(msg, file string) {
	f, err := os.OpenFile(file, os.O_RDWR|os.O_CREATE, 0666)
	if err != nil {
		panic(err.Error())
	}
	defer f.Close()
	by, err := ioutil.ReadAll(f)
	if err != nil {
		panic(err.Error())
	}
	gou.Infof("dumpfile %s  %s\n%s", msg, file, string(by))
}

func ListObjsAndFolders(t TestingT, store cloudstorage.Store) {

	Clearstore(t, store)

	createObjects := func(names []string) {
		for _, n := range names {
			obj, err := store.NewObject(n)
			assert.Equal(t, nil, err)
			if obj == nil {
				continue
			}

			f1, err := obj.Open(cloudstorage.ReadWrite)
			assert.Equal(t, nil, err)
			assert.NotEqual(t, nil, f1)

			testcsv := "12345\n"

			w1 := bufio.NewWriter(f1)
			_, err = w1.WriteString(testcsv)
			assert.Equal(t, nil, err)
			w1.Flush()

			err = obj.Close()
			assert.Equal(t, nil, err)
		}
	}

	// Create 5 objects in each of 3 folders
	// ie 15 objects
	folders := []string{"a", "b", "c"}
	names := []string{}
	for _, folder := range folders {
		for i := 0; i < 5; i++ {
			n := fmt.Sprintf("list-test/%s/test%d.csv", folder, i)
			names = append(names, n)
		}
	}

	sort.Strings(names)

	createObjects(names)

	q := cloudstorage.NewQuery("list-test/")
	q.Sorted()
	iter, _ := store.Objects(context.Background(), q)
	objs, err := cloudstorage.ObjectsAll(iter)
	assert.Equal(t, nil, err)
	assert.Equal(t, 15, len(objs), "incorrect list len. wanted 15 got %d", len(objs))

	// Now we are going to re-run this test using store.List() instead of store.Objects()
	q = cloudstorage.NewQuery("list-test/")
	q.Sorted()
	objResp, err := store.List(context.Background(), q)
	assert.Equal(t, nil, err)
	assert.Equal(t, 15, len(objResp.Objects), "incorrect list len. wanted 15 got %d", len(objResp.Objects))

	// Now we are going to re-run this test using an Object Iterator
	// that uses store.List() instead of store.Objects()
	q = cloudstorage.NewQuery("list-test/")
	q.Sorted()
	iter = cloudstorage.NewObjectPageIterator(context.Background(), store, q)
	objs = make(cloudstorage.Objects, 0)
	i := 0
	for {
		o, err := iter.Next()
		if err == iterator.Done {
			break
		}
		objs = append(objs, o)
		//u.Debugf("iter i=%d  len names=%v", i, len(names))
		//u.Infof("2 %d found %v expect %v", i, o.Name(), names[i])
		assert.Equal(t, names[i], o.Name(), "unexpected name.")
		i++
	}
	assert.Equal(t, 15, len(objs), "incorrect list len. wanted 15 got %d", len(objs))

	q = cloudstorage.NewQuery("list-test/b")
	q.Sorted()
	iter, _ = store.Objects(context.Background(), q)
	objs, err = cloudstorage.ObjectsAll(iter)
	assert.Equal(t, nil, err)
	assert.Equal(t, 5, len(objs), "incorrect list len. wanted 5 got %d", len(objs))

	for i, o := range objs {
		assert.Equal(t, names[i+5], o.Name(), "unexpected name.")
	}

	// test with iterator
	iter, _ = store.Objects(context.Background(), q)
	objs = make(cloudstorage.Objects, 0)
	i = 0
	for {
		o, err := iter.Next()
		if err == iterator.Done {
			break
		}
		objs = append(objs, o)
		//t.Logf("%d found %v", i, o.Name())
		assert.Equal(t, names[i+5], o.Name(), "unexpected name.")
		i++
	}

	assert.Equal(t, 5, len(objs), "incorrect list len.")

	q = cloudstorage.NewQueryForFolders("list-test/")
	folders, err = store.Folders(context.Background(), q)
	assert.Equal(t, nil, err)
	assert.Equal(t, 3, len(folders), "incorrect list len. wanted 3 folders. %v", folders)
	sort.Strings(folders)
	assert.Equal(t, []string{"list-test/a/", "list-test/b/", "list-test/c/"}, folders)

	foldersInput := []string{"a/a2", "b/b1", "b/b2"}
	names = []string{}
	for _, folder := range foldersInput {
		for i := 0; i < 2; i++ {
			n := fmt.Sprintf("list-test/%s/test%d.csv", folder, i)
			names = append(names, n)
		}
	}

	sort.Strings(names)

	createObjects(names)

	q = cloudstorage.NewQueryForFolders("list-test/")
	folders, err = store.Folders(context.Background(), q)
	assert.Equal(t, nil, err)
	assert.Equal(t, 3, len(folders), "incorrect list len. wanted 3 folders. %v", folders)
	assert.Equal(t, []string{"list-test/a/", "list-test/b/", "list-test/c/"}, folders)

	q = cloudstorage.NewQueryForFolders("list-test/b/")
	folders, err = store.Folders(context.Background(), q)
	assert.Equal(t, nil, err)
	assert.Equal(t, 2, len(folders), "incorrect list len. wanted 2 folders. %v", folders)
	assert.Equal(t, []string{"list-test/b/b1/", "list-test/b/b2/"}, folders)
}

func Truncate(t TestingT, store cloudstorage.Store) {

	Clearstore(t, store)

	// Create a new object and write to it.
	obj, err := store.NewObject("test.csv")
	assert.Equal(t, nil, err)

	f1, err := obj.Open(cloudstorage.ReadWrite)
	assert.Equal(t, nil, err)
	assert.NotEqual(t, nil, f1, "the file was nil")

	testcsv := "Year,Make,Model\n2003,VW,EuroVan\n2001,Ford,Ranger\n"

	w1 := bufio.NewWriter(f1)
	n1, err := w1.WriteString(testcsv)
	assert.Equal(t, nil, err, "error. %d", n1)
	w1.Flush()

	err = obj.Close()
	assert.Equal(t, nil, err)

	// get the object and replace it...
	newtestcsv := "Year,Make,Model\n2013,VW,Jetta\n"
	obj2, err := store.Get(context.Background(), "test.csv")
	assert.Equal(t, nil, err)

	f2, err := obj2.Open(cloudstorage.ReadWrite)
	assert.Equal(t, nil, err)
	assert.NotEqual(t, nil, f2, "the file was nil")

	// Truncating the file will zero out the file
	f2.Truncate(0)
	// We also want to start writing from the beginning of the file
	f2.Seek(0, 0)

	w2 := bufio.NewWriter(f2)
	n2, err := w2.WriteString(newtestcsv)
	assert.Equal(t, nil, err, "error. %d", n2)
	w2.Flush()

	err = obj2.Close()
	assert.Equal(t, nil, err)

	// Read the object back out of the cloud storage.
	obj3, err := store.Get(context.Background(), "test.csv")
	assert.Equal(t, nil, err)

	f3, err := obj3.Open(cloudstorage.ReadOnly)
	assert.Equal(t, nil, err)

	bytes, err := ioutil.ReadAll(f3)
	assert.Equal(t, nil, err)

	assert.Equal(t, newtestcsv, string(bytes), "not the rows we expected.")
}

func NewObjectWithExisting(t TestingT, store cloudstorage.Store) {

	Clearstore(t, store)

	// Create a new object and write to it.
	obj, err := store.NewObject("test.csv")
	assert.Equal(t, nil, err)

	f, err := obj.Open(cloudstorage.ReadWrite)
	assert.Equal(t, nil, err)
	assert.NotEqual(t, nil, f, "the file was nil")

	testcsv := "Year,Make,Model\n2003,VW,EuroVan\n2001,Ford,Ranger\n"

	w := bufio.NewWriter(f)
	n, err := w.WriteString(testcsv)
	assert.Equal(t, nil, err, "error. %d", n)
	w.Flush()

	err = obj.Close()
	assert.Equal(t, nil, err)

	// Ensure calling NewObject on an existing object returns an error,
	// because the object exits.
	obj2, err := store.NewObject("test.csv")
	assert.Equal(t, cloudstorage.ErrObjectExists, err, "error.")
	assert.Equal(t, nil, obj2, "object should be nil.")

	// Read the object back out of the cloud storage.
	obj3, err := store.Get(context.Background(), "test.csv")
	assert.Equal(t, nil, err)

	f3, err := obj3.Open(cloudstorage.ReadOnly)
	assert.Equal(t, nil, err)

	bytes, err := ioutil.ReadAll(f3)
	assert.Equal(t, nil, err)

	assert.Equal(t, testcsv, string(bytes))
}

func TestReadWriteCloser(t TestingT, store cloudstorage.Store) {

	Clearstore(t, store)

	gou.Debugf("starting TestReadWriteCloser")
	object := "prefix/iorw.test"
	data := fmt.Sprintf("pid:%v:time:%v", os.Getpid(), time.Now().Nanosecond())

	wc, err := store.NewWriter(object, nil)
	assert.Equal(t, nil, err)
	buf1 := bytes.NewBufferString(data)
	_, err = buf1.WriteTo(wc)
	assert.Equal(t, nil, err)
	err = wc.Close()
	assert.Equal(t, nil, err)
	time.Sleep(time.Millisecond * 100)

	rc, err := store.NewReader(object)
	assert.Equal(t, nil, err)
	if rc == nil {
		t.Fatalf("could not create reader")
		return
	}
	buf2 := bytes.Buffer{}
	_, err = buf2.ReadFrom(rc)
	assert.Equal(t, nil, err)
	assert.Equal(t, data, buf2.String(), "round trip data don't match")
}
