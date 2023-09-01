use std::any::Any;
use std::error;
use std::error::Error;
use std::io::prelude::*;
use std::io::BufReader;
use std::fs::File;
use std::path::Path;
use std::string;
use std::thread;
use std::sync::mpsc::channel;
use std::sync::mpsc::sync_channel;
use std::time::Duration;
use std::slice::Iter;
use std::str::Chars;
// use regex::Regex;

use notify::Event;
use notify::event::MetadataKind;
use notify::{Watcher, RecommendedWatcher, RecursiveMode};


fn main() -> std::io::Result<()> {

    // API handler will first try the request and authenticate only when 401 is returned.

    // Setup notifiers
    // Request logs from API for this generation
    // -> after logs are fetched once, do not fetch them again
    // -> Read the length of each log item
    // -> Push logs appending only from byte
    // After initial push, when notify finds changes to file, append from byte only and push entire file


    let f = File::open("go.mod")?;
    let mut reader = BufReader::new(f);
    let mut line = String::new();
    let len = reader.read_line(&mut line)?;
    println!("First line is {len} bytes long");



    let f = File::open("log1.txt")?;
    let mut reader = BufReader::new(f);
    let len_of_log1_txt = reader.read_to_string(&mut line)?;
    println!("log1.txt is {len_of_log1_txt} bytes long");

    let f = File::open("log2.txt")?;
    let mut reader = BufReader::new(f);
    let len_of_log2_txt = reader.read_to_string(&mut line)?;
    println!("log2.txt before log1.txt len is taken into account is {len_of_log2_txt} bytes long");

    let f = File::open("log2.txt")?;
    let mut reader = BufReader::new(f);
    reader.seek_relative(len_of_log1_txt.try_into().unwrap()).expect("the hell!");
    let entirelen = reader.read_to_string(&mut line)?;
    println!("log2.txt after log1.txt len is taken into account is {entirelen} bytes long");


    setup_notifier();
    ch();

    Ok(())
    // Ok(())


}

fn touch_file() {

}

fn read_on_event(event: Event) {
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
    let dirname = dir(filepath);
    let filename= base(filepath);
    let name = parse_name(&filename);

    if name.is_ok(){
        println!("event {:?}", event);
        let n = name.unwrap();
        println!("task_name={}, rerun_id={}, dir={}", n.task_name, n.rerun_id, dirname);
    }
}



fn is_write_event(event: &Event) -> bool {
    // let kind = event.kind;
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

fn is_log_file(filepath: &str) -> bool {
    filepath.ends_with(".out")
}

#[derive(Debug)]
struct LogHandler {
    task_name: String,
    rerun_id: String,
    uuid: String
}


fn base(filepath: &str) -> String {
    let items: Vec<&str> = filepath.split("/").collect();
    items.last().unwrap().to_string()
}

fn dir(filepath: &str) -> String {
    let mut items: Vec<&str> = filepath.split("/").collect();
    items.pop();
    items.join("/")
}

fn has_three_dots(filename: &str) -> bool {
    let items: Vec<&str> = filename.split(".").collect();
    return items.len() == 4
}



fn parse_name(filename: &str) -> Result<LogHandler, &str> {
    let filename_parts: Vec<&str> = filename.split(".").collect();
    if filename_parts.len() != 4 {
        Err("Not correct number of dots")
    } else {
        let task_name = filename_parts[0];
        let rerun_id = filename_parts[1];
        let uuid = filename_parts[2];

        let is_rerun_id_an_int = rerun_id.parse::<u32>().is_ok();
        let is_uuid = is_valid_uuid(uuid);

        if !is_rerun_id_an_int {
            Err("Wrong rerun_id format")
        } else if !is_uuid {
            Err("Wrong uuid format")
        } else {
            Ok(LogHandler{
                task_name: task_name.to_string(),
                rerun_id: rerun_id.to_string(),
                uuid: uuid.to_string()
            })
        }
    }
}

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
                // UUID format is 8-4-4-4-12
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

fn is_all_alphanumeric(ch: Chars<'_>) -> bool{
    let b: bool = true;
    for a in ch {
        if !a.is_alphanumeric(){
            return false
        }
    }
    b
}

fn setup_notifier() -> core::result::Result<(), notify::Error> {

    // Automatically select the best implementation for your platform.
    let mut watcher = notify::recommended_watcher(|res| {
        match res {
        Ok(event) => read_on_event(event),
        Err(e) => println!("watch error: {:?}", e),
        }
    })?;

    // Watch only the files in the directory
    watcher.watch(Path::new("logfiles"), RecursiveMode::NonRecursive)?;



    // Name the files that are in the directory and name when they change

    thread::sleep(Duration::from_secs(600));


    Ok(())
}

fn ch() {

    let (tx, rx) = sync_channel(3);

    for i in 0..10 {
        // It would be the same without thread and clone here
        // since there will still be one `tx` left.
        let tx = tx.clone();
        // cloned tx dropped within thread

        let index = i.to_string();
        thread::spawn(move || tx.send(format!("{index} ok")).unwrap());
    }

    // Drop the last sender to stop `rx` waiting for message.
    // The program will not complete if we comment this out.
    // **All** `tx` needs to be dropped for `rx` to have `Err`.
    drop(tx);

    // Unbounded receiver waiting for all senders to complete.
    while let Ok(msg) = rx.recv() {
        println!("{msg}");
    }

    println!("completed");
}