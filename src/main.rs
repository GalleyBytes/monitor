use std::env::consts::OS;
use std::io::{Error, ErrorKind};
use std::io::prelude::*;
use std::io::BufReader;
use std::fs::File;
use std::path::Path;
use std::{thread, os, env};
use std::sync::mpsc::channel;
use std::sync::mpsc::sync_channel;
use std::time::Duration;
use std::slice::Iter;
use std::str::Chars;
// use regex::Regex;
use std::collections::HashMap;



use notify::Event;
use notify::event::MetadataKind;
use notify::{Watcher, RecommendedWatcher, RecursiveMode};


#[derive(Debug)]
struct Log {
    pub filepath: String,
    pub uuid: String,
    pub content: String
}

impl Log {
    fn new(filepath: &str, uuid: &str) -> std::io::Result<Log> {
        let mut f = File::open(filepath)?;
        let mut buffer = String::new();
        f.read_to_string(&mut buffer)?;
        Ok(Log{
            filepath: filepath.to_string(),
            uuid: uuid.to_string(),
            content: buffer
        })
    }
}

#[derive(Debug)]
struct FileHandler {
    log: Log,
}

impl FileHandler {
    fn new(filepath: &str) -> std::io::Result<FileHandler> {
        let filename= base(filepath);
        let filename_parts: Vec<&str> = filename.split(".").collect();
        if filename_parts.len() != 4 {
            Err(Error::new(ErrorKind::Other, "Not correct number of dots"))
            // Err(Error())
        } else {
            // let task_name = filename_parts[0];
            let rerun_id = filename_parts[1];
            let uuid = filename_parts[2];

            let is_rerun_id_an_int = rerun_id.parse::<u32>().is_ok();
            let is_uuid = is_valid_uuid(uuid);

            if !is_rerun_id_an_int {
                Err(Error::new(ErrorKind::Other, "Wrong rerun_id format"))
            } else if !is_uuid {
                Err(Error::new(ErrorKind::Other, "Wrong uuid format"))
            } else {
                let log = Log::new(filepath, uuid)?;
                Ok(FileHandler{log})
            }
        }
    }

    fn conent(&self) -> &str {
        &self.log.content
    }

    fn uuid(&self) -> &str {
        &self.log.uuid
    }

    fn filepath(&self) -> &str {
        &self.log.filepath
    }

    #[tokio::main]
    async fn upload(&self, url: &str) -> Result<(), Box<dyn std::error::Error>> {
        let mut map = HashMap::new();
        map.insert("content", self.conent());
        map.insert("uuid", self.uuid());

        let client = reqwest::Client::new();
        let res = client.post(url)
            .json(&map)
            .send()
            .await?
            .text()
            .await?;

        // let resp = reqwest::blocking::get("")?
        //     .json::<HashMap<String, String>>()?;
        println!("{}", res);
        Ok(())
    }


}


/// Check if is a write event. Ignore events like metadata changes.
fn is_write_event(event: &Event) -> bool {
    if let notify::EventKind::Modify(modify_kind) = event.kind {
        match modify_kind {
            notify::event::ModifyKind::Data(_) => true,
            _ => false
        }
    } else{
        // false
        event.kind.is_create()
    }
}


/// Extract the filename from a filepath ie the last item after the last '/'
fn base(filepath: &str) -> String {
    let items: Vec<&str> = filepath.split("/").collect();
    items.last().unwrap().to_string()
}


/// Check if a &str is a uuid
fn is_valid_uuid(uuid: &str) -> bool {
    // example: ad7e47a1-15a1-44f8-b4cb-924b02e1cc89
    if uuid.len() != 36 {
        return false;
    } else {
        let uuid_items: Vec<&str> = uuid.split("-").collect();
        if uuid_items.len() != 5 {
            return false;
        } else {
            for (index, &item) in uuid_items.iter().enumerate() {
                // UUID format is 8-4-4-4-12 chars + alphanumeric
                let mut is_correct_chars =  true;
                is_correct_chars = match index {
                    0 => item.chars().count() == 8 && is_all_alphanumeric(item.chars()),
                    1 => item.chars().count() == 4 && is_all_alphanumeric(item.chars()),
                    2 => item.chars().count() == 4 && is_all_alphanumeric(item.chars()),
                    3 => item.chars().count() == 4 && is_all_alphanumeric(item.chars()),
                    4 => item.chars().count() == 12 && is_all_alphanumeric(item.chars()),
                    _ => false
                };
                if !is_correct_chars {
                    return false;
                }
            }
        }
    }
    true
}

/// Check that all chars in an array of chars are alphanumric
fn is_all_alphanumeric(ch: Chars<'_>) -> bool{
    let b: bool = true;
    for a in ch {
        if !a.is_alphanumeric(){
            return false
        }
    }
    b
}

/// For every log event, send a POST request of logs
fn post_on_event(event: Event, url: &str) {
    if !is_write_event(&event) {
        return
    }
    if event.paths.len() != 1 {
        return
    }

    let filepath = event.paths[0].to_str().unwrap();
    if !filepath.ends_with(".out") {
        return
    }
    let file_handler_result = FileHandler::new(filepath);
    if file_handler_result.is_err() {
        println!("File: {}, Error:{:?}", filepath, file_handler_result.unwrap_err());
        return
    }
    let file_handler = file_handler_result.unwrap();


    println!("event {:?}", event);
    println!("File: {}\nuuid: {}", file_handler.filepath(), file_handler.uuid());
    println!("-CONTENT-\n{}",file_handler.conent());

    file_handler.upload(url);

}

/// Setup and start fs notify.
/// For each nitification, send a http POST request with full log data.
fn run_notifier(url: String, logpath: String) -> core::result::Result<(), notify::Error> {

    // Convenience method for creating the RecommendedWatcher for the current platform in immediate mode
    let mut watcher = notify::recommended_watcher(move |res| {
        match res {
        Ok(event) => {
            post_on_event(event, url.as_str())
        },
        Err(e) => println!("watch error: {:?}", e),
        }
    })?;

    watcher.watch(Path::new(logpath.as_str()), RecursiveMode::NonRecursive)?;
    println!("Watching started...");
    loop{
        thread::sleep(Duration::from_secs(600));
    };
}


fn main() {
    let url = env::var("TFO_API_LOG_URL").expect("$TFO_API_LOG_URL is not set");
    let logpath = env::var("LOG_PATH").expect("$LOG_PATH is not set");

    run_notifier(url, logpath).expect("Failed to start notifier");


}