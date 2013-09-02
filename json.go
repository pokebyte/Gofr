/*****************************************************************************
 **
 ** PerFeediem
 ** https://github.com/melllvar/perfeediem
 ** Copyright (C) 2013 Akop Karapetyan
 **
 ** This program is free software; you can redistribute it and/or modify
 ** it under the terms of the GNU General Public License as published by
 ** the Free Software Foundation; either version 2 of the License, or
 ** (at your option) any later version.
 **
 ** This program is distributed in the hope that it will be useful,
 ** but WITHOUT ANY WARRANTY; without even the implied warranty of
 ** MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
 ** GNU General Public License for more details.
 **
 ** You should have received a copy of the GNU General Public License
 ** along with this program; if not, write to the Free Software
 ** Foundation, Inc., 675 Mass Ave, Cambridge, MA 02139, USA.
 **
 ******************************************************************************
 */
 
package perfeediem

import (
  "appengine"
  "appengine/blobstore"
  "appengine/datastore"
  "appengine/taskqueue"
  "appengine/urlfetch"
  "io/ioutil"
  "net/url"
  "opml"
  "regexp"
  "rss"
  "storage"
  "strconv"
  "strings"
  "unicode/utf8"
)

var validProperties = map[string]bool {
  "unread": true,
  "read":   true,
  "star":   true,
  "like":   true,
}

func registerJson() {
  RegisterJSONRoute("/subscriptions", subscriptions)
  RegisterJSONRoute("/articles",      articles)
  RegisterJSONRoute("/createFolder",  createFolder)
  RegisterJSONRoute("/rename",        rename)
  RegisterJSONRoute("/setProperty",   setProperty)
  RegisterJSONRoute("/subscribe",     subscribe)
  RegisterJSONRoute("/unsubscribe",   unsubscribe)
  RegisterJSONRoute("/authUpload",    authUpload)
  RegisterJSONRoute("/import",        importOPML)
  RegisterJSONRoute("/markAllAsRead", markAllAsRead)
}

func subscriptions(pfc *PFContext) (interface{}, error) {
  userID := storage.UserID(pfc.User.ID)

  return storage.NewUserSubscriptions(pfc.Context, userID)
}

func articles(pfc *PFContext) (interface{}, error) {
  r := pfc.R
  userID := storage.UserID(pfc.User.ID)

  filter := storage.ArticleFilter {
    SubscriptionID: r.FormValue("subscription"),
    FolderID: r.FormValue("folder"),
    UserID: userID,
  }

  if filterProperty := r.FormValue("filter"); validProperties[filterProperty] {
    filter.Property = filterProperty
  }

  return storage.NewArticlePage(pfc.Context, filter, r.FormValue("continue"))
}

func createFolder(pfc *PFContext) (interface{}, error) {
  r := pfc.R
  userID := storage.UserID(pfc.User.ID)

  title := r.PostFormValue("folderName")
  if title == "" {
    return nil, NewReadableError(_l("Missing folder name"), nil)
  }

  if utf8.RuneCountInString(title) > 200 {
    return nil, NewReadableError(_l("Folder name is too long"), nil)
  }

  if exists, err := storage.IsFolderDuplicate(pfc.Context, userID, title); err != nil {
    return nil, err
  } else if exists {
    return nil, NewReadableError(_l("A folder with that name already exists"), nil)
  }

  if err := storage.CreateFolder(pfc.Context, userID, title); err != nil {
    return nil, NewReadableError(_l("An error occurred while adding the new folder"), &err)
  }

  return storage.NewUserSubscriptions(pfc.Context, userID)
}

func rename(pfc *PFContext) (interface{}, error) {
  r := pfc.R
  userID := storage.UserID(pfc.User.ID)

  title := r.PostFormValue("title")
  if title == "" {
    return nil, NewReadableError(_l("Name not specified"), nil)
  }

  folderID := r.PostFormValue("folder")

  if subscriptionID := r.PostFormValue("subscription"); subscriptionID != "" {
    // Rename subscription
    ref := storage.SubscriptionRef {
      FolderRef: storage.FolderRef {
        UserID: userID,
        FolderID: folderID,
      },
      SubscriptionID: subscriptionID,
    }
    if err := storage.RenameSubscription(pfc.Context, ref, title); err != nil {
      return nil, NewReadableError(_l("Error renaming folder"), &err)
    }
  } else if folderID != "" {
    // Rename folder
    if exists, err := storage.IsFolderDuplicate(pfc.Context, userID, title); err != nil {
      return nil, err
    } else if exists {
      return nil, NewReadableError(_l("A folder with that name already exists"), nil)
    }

    if err := storage.RenameFolder(pfc.Context, userID, folderID, title); err != nil {
      return nil, NewReadableError(_l("Error renaming folder"), &err)
    }
  } else {
    return nil, NewReadableError(_l("Nothing to rename"), nil)
  }

  return storage.NewUserSubscriptions(pfc.Context, userID)
}

func setProperty(pfc *PFContext) (interface{}, error) {
  r := pfc.R
  userID := storage.UserID(pfc.User.ID)

  folderID := r.PostFormValue("folder")
  subscriptionID := r.PostFormValue("subscription")
  articleID := r.PostFormValue("article")
  propertyName := r.PostFormValue("property")
  propertyValue := r.PostFormValue("set") == "true"

  if articleID == "" || subscriptionID == "" {
    return nil, NewReadableError(_l("Article not found"), nil)
  }

  if !validProperties[propertyName] {
    return nil, NewReadableError(_l("Property not valid"), nil)
  }

  ref := storage.ArticleRef {
    SubscriptionRef: storage.SubscriptionRef {
      FolderRef: storage.FolderRef {
        UserID: userID,
        FolderID: folderID,
      },
      SubscriptionID: subscriptionID,
    },
    ArticleID: articleID,
  }

  if properties, err := storage.SetProperty(pfc.Context, ref, propertyName, propertyValue); err != nil {
    return nil, NewReadableError(_l("Error updating article"), &err)
  } else {
    return properties, nil
  }
}

func subscribe(pfc *PFContext) (interface{}, error) {
  c := pfc.C
  r := pfc.R
  userID := storage.UserID(pfc.User.ID)

  subscriptionURL := r.PostFormValue("url")
  folderId := r.PostFormValue("folder")

  if subscriptionURL == "" {
    return nil, NewReadableError(_l("Missing URL"), nil)
  } else if _, err := url.ParseRequestURI(subscriptionURL); err != nil {
    return nil, NewReadableError(_l("URL is not valid"), &err)
  }

  if folderId != "" {
    ref := storage.FolderRef {
      UserID: userID,
      FolderID: folderId,
    }

    if exists, err := storage.FolderExists(pfc.Context, ref); err != nil {
      return nil, err
    } else if !exists {
      return nil, NewReadableError(_l("Folder not found"), nil)
    }
  }

  if exists, err := storage.IsFeedAvailable(pfc.Context, subscriptionURL); err != nil {
    return nil, err
  } else if !exists {
    // Not a known feed URL
    // Match it against a list of known WWW links

    if feedURL, err := storage.WebToFeedURL(pfc.Context, subscriptionURL); err != nil {
      return nil, err
    } else if feedURL != "" {
      subscriptionURL = feedURL
    } else {
      // Still nothing
      // Add/remove 'www' to/from URL and try again

      var modifiedURL string
      if re := regexp.MustCompile(`://www\.`); re.MatchString(subscriptionURL) {
        modifiedURL = re.ReplaceAllString(subscriptionURL, "://")
      } else {
        re = regexp.MustCompile(`://`)
        modifiedURL = re.ReplaceAllString(subscriptionURL, "://www.")
      }

      if feedURL, err := storage.WebToFeedURL(pfc.Context, modifiedURL); err != nil {
        return nil, err
      } else if feedURL != "" {
        subscriptionURL = feedURL
      }
    }
  }

  if subscribed, err := storage.IsSubscriptionDuplicate(pfc.Context, userID, subscriptionURL); err != nil {
    return nil, err
  } else if subscribed {
    return nil, NewReadableError(_l("You are already subscribed"), nil)
  }

  // At this point, the URL may have been re-written, so we check again
  if exists, err := storage.IsFeedAvailable(pfc.Context, subscriptionURL); err != nil {
    return nil, err
  } else if !exists {
    // Don't have the locally - fetch it
    client := urlfetch.Client(c)
    if response, err := client.Get(subscriptionURL); err != nil {
      return nil, NewReadableError(_l("An error occurred while downloading the feed"), &err)
    } else {
      defer response.Body.Close()
      
      var body string
      if bytes, err := ioutil.ReadAll(response.Body); err != nil {
        return nil, NewReadableError(_l("An error occurred while reading the feed"), &err)
      } else {
        body = string(bytes)
      }

      reader := strings.NewReader(body)
      if _, err := rss.UnmarshalStream(subscriptionURL, reader); err != nil {
        // Parse failed. Assume it's an HTML document and 
        // try to pull out an RSS <link />

        if linkURL := rss.ExtractRSSLink(body); linkURL == "" {
          return nil, NewReadableError(_l("RSS content not found"), &err)
        } else {
          subscriptionURL = linkURL
        }
      }
    }
  }

  task := taskqueue.NewPOSTTask("/tasks/subscribe", url.Values {
    "url": { subscriptionURL },
    "folderId": { folderId },
    "userID": { pfc.User.ID },
  })

  if _, err := taskqueue.Add(c, task, ""); err != nil {
    return nil, NewReadableError(_l("Cannot subscribe - too busy"), &err)
  }

  return _l("Your subscription has been queued for addition."), nil
}

func unsubscribe(pfc *PFContext) (interface{}, error) {
  c := pfc.C
  r := pfc.R
  userID := storage.UserID(pfc.User.ID)

  subscriptionID := r.PostFormValue("subscription")
  folderID := r.PostFormValue("folder")

  var task *taskqueue.Task
  if subscriptionID != "" {
    // Remove a subscription
    ref := storage.SubscriptionRef {
      FolderRef: storage.FolderRef {
        UserID: userID,
        FolderID: folderID,
      },
      SubscriptionID: subscriptionID,
    }

    if exists, err := storage.SubscriptionExists(pfc.Context, ref); err != nil {
      return nil, err
    } else if !exists {
      return nil, NewReadableError(_l("Subscription not found"), nil)
    }
  } else if folderID != "" {
    // Remove a folder
    ref := storage.FolderRef {
      UserID: userID,
      FolderID: folderID,
    }

    if exists, err := storage.FolderExists(pfc.Context, ref); err != nil {
      return nil, err
    } else if !exists {
      return nil, NewReadableError(_l("Folder not found"), nil)
    }
  } else {
    return nil, NewReadableError(_l("Item not found"), nil)
  }

  task = taskqueue.NewPOSTTask("/tasks/unsubscribe", url.Values {
    "userID": { pfc.User.ID },
    "subscriptionID": { subscriptionID },
    "folderID": { folderID },
  })

  if _, err := taskqueue.Add(c, task, ""); err != nil {
    return nil, NewReadableError(_l("Cannot unsubscribe at the moment - try again later"), &err)
  }

  return _l("Queued for deletion"), nil
}

func authUpload(pfc *PFContext) (interface{}, error) {
  c := pfc.C

  if uploadURL, err := blobstore.UploadURL(c, "/import", nil); err != nil {
    return nil, err
  } else {
    return map[string]string { "uploadUrl": uploadURL.String() }, nil
  }
}

func importOPML(pfc *PFContext) (interface{}, error) {
  c := pfc.C
  r := pfc.R

  blobs, _, err := blobstore.ParseUpload(r)
  if err != nil {
    return nil, NewReadableError(_l("Error receiving file"), &err)
  }

  var blobKey appengine.BlobKey
  if blobInfos := blobs["opml"]; len(blobInfos) == 0 {
    return nil, NewReadableError(_l("File not uploaded"), nil)
  } else {
    blobKey = blobInfos[0].BlobKey
    reader := blobstore.NewReader(c, blobKey)

    var doc opml.Document
    if err := opml.Parse(reader, &doc); err != nil {
      if err := blobstore.Delete(c, blobKey); err != nil {
        c.Warningf("Error deleting blob (key %s): %s", blobKey, err)
      }

      return nil, NewReadableError(_l("Error reading OPML file"), &err)
    }
  }

  task := taskqueue.NewPOSTTask("/tasks/import", url.Values {
    "opmlBlobKey": { string(blobKey) },
    "userID": { pfc.User.ID },
  })

  if _, err := taskqueue.Add(c, task, ""); err != nil {
    // Remove the blob
    if err := blobstore.Delete(c, blobKey); err != nil {
      c.Warningf("Error deleting blob (key %s): %s", blobKey, err)
    }

    return nil, NewReadableError(_l("Error initiating import"), &err)
  }

  return _l("Subscriptions are being imported"), nil
}

func markAllAsRead(pfc *PFContext) (interface{}, error) {
  c := pfc.C
  r := pfc.R
  userID := storage.UserID(pfc.User.ID)

  subscriptionID := r.PostFormValue("subscription")
  folderID := r.PostFormValue("folder")

  if subscriptionID != "" {
    ref := storage.SubscriptionRef {
      FolderRef: storage.FolderRef {
        UserID: userID,
        FolderID: folderID,
      },
      SubscriptionID: subscriptionID,
    }
    if exists, err := storage.SubscriptionExists(pfc.Context, ref); err != nil {
      return nil, err
    } else if !exists {
      return nil, NewReadableError(_l("Subscription not found"), nil)
    }
  } else if folderID != "" {
    ref := storage.FolderRef {
      UserID: userID,
      FolderID: folderID,
    }

    if exists, err := storage.FolderExists(pfc.Context, ref); err != nil {
      return nil, err
    } else if !exists {
      return nil, NewReadableError(_l("Folder not found"), nil)
    }
  }

  task := taskqueue.NewPOSTTask("/tasks/markAllAsRead", url.Values {
    "userID": { pfc.User.ID },
    "subscriptionID": { subscriptionID },
    "folderID": { folderID },
  })

  if _, err := taskqueue.Add(c, task, ""); err != nil {
    return nil, err
  }

  return _l("Marking items as unread…"), nil
}

func unformatId(formattedId string) (string, int64, error) {
  if parts := strings.SplitN(formattedId, "://", 2); len(parts) == 2 {
    if id, err := strconv.ParseInt(parts[1], 36, 64); err == nil {
      return parts[0], id, nil
    } else {
      return parts[0], 0, nil
    }
  }

  return "", 0, NewReadableError(_l("Missing identifier"), nil)
}

func subscriptionKeyForURL(c appengine.Context, feedURL string, userKey *datastore.Key) (*datastore.Key, error) {
  feedKey := datastore.NewKey(c, "Feed", feedURL, 0, nil)
  q := datastore.NewQuery("Subscription").Ancestor(userKey).Filter("Feed =", feedKey).KeysOnly().Limit(1)

  if subKeys, err := q.GetAll(c, nil); err != nil {
    return nil, err
  } else if len(subKeys) > 0 {
    return subKeys[0], nil
  } else {
    return nil, nil
  }
}
