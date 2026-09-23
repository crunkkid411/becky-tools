
# Workflows

## 1. New Project

- Launch Vegas Pro 18
- Save the file to the same folder where the footage is located, and name the Vegas Pro file the same name as the folder unless Jordan specifies otherwise. See "File Structure" below for examples and rules.
- Import the footage onto the timeline.
- Vegas Pro automatically set's the Project frame rate and dimensions based on the first video clip imported
- If there are multiple frame rates or dimensions, whichever clip is 29.97 fps is always the one we want the project to match.
- If Standard YouTube video, import the 29.97 fps widescreen video first
- If creating vertical content, import the 29.97 fps vertical video first
- Ensure all footage on the timeline is in chronological order unless Jordan specified otherwise
- If videos all contain the same speaker(s), all the files go on the same video and audio track
- If there are multiple sources (example - Talking head style footage of Jordan, but also some vertical clips or widescreen clips from a different person / filming environment), then the secondary media types get all belong on a separate video and audio track
- Save the project again

## 2. Create Shorts from existing video

1. File -> Properties OR use "Alt + Enter". This opens a popup box
- Change "Width" to 1080 and change "Height" to 1920
- Close the popup box

2. File -> "Save As" OR use "Ctrl + Shift + S"

3. Select all video and images on the timeline

4. Run the "Match Output Aspect" script

5. Visually verify that every clip is framed properly
- If the subject of a clip is not centered, it must be manually adjusted for that clip.
- Use Vegas's Pan/Crop tool in order to adjust left / right and up / down if necessary
- OR use Vegas's "Picture in Picture" Video Event FX if that is easier for you to work with (they both achieve the same end result)

6. If Jordan gave specific clips to get from the video, create Regions around the specified subjects

7. Render all regions. Name them the name of the short's title Jordan Provides

## Rules

- **Original files are NEVER deleted or rewritten.** this is CRITICAL and non-negotiable

### Folder Structure
- I've **mostly** used the same folder structure for years, where there are any inconsistencies it is critical that we DO NOT change anything
- many projects rely on specific file locations; updating or correcting old erroneous naming schemas will BREAK anythign which relies upon that location
- Agents must NEVER change or move files or footage; this is the archive of my last 10+ years of work. Files are to be referenced, or copied if necessary but the originals NEVER change

CORRECT naming schema; "year"\"month" (number_name)\"day"\"video_title"
EXAMPLE: "X:\Videos\2026\09_sept\8_goodbye_youtube"
- most video assets are contained in that folder, including .veg, .xml, .json, etc
- Use kebab-case: `goodbye-youtube.mp4`
- No spaces, no special characters in filenames

- When creating a new Vegas Pro Project, save the file to the same folder where the footage is located, and name the Vegas Pro file the same name as the folder unless Jordan specifies otherwise.
Example: "X:\Videos\2026\09_sept\8_goodbye_youtube\goodbye-youtube.veg"
- When repurposing videos for vertical short form content, we Save the project with "-VERTICAL" at the end so the original .veg file remains intact
Example: "X:\Videos\2026\09_sept\8_goodbye_youtube\goodbye-youtube-VERTICAL.veg"

All video renders go to the "rendered" folder for that month
EXAMPLE: "X:\Videos\2026\09_sept\rendered\goodbye-youtube.mp4"

### Older Videos
- Before I learned about AI, I did not use kebab-case in my naming schema. We cannot change it now without breaking certain projects which re-use footage and assets.
- I also sometimes would name "day_video_title" in one folder
EXAMPLE: `X:\Videos\2022\1 Jan\3 Emos React`

When repurposing, we can export an .xml file for Resolve. When we do, place it in a "Resolve" subfolder. Ensure NOT to copy the assets as they already exist in the same folder
EXAMPLE: `X:\Videos\2022\1 Jan\3 Emos React\Resolve`

### Missing Assets
- I used to edit from my primary "C:\" drive, now the "X:\" drive is where I edit from. This can cause asset issues, but only the drive portion has changed.
EXAMPLE: `C:\Users\only1\Videos\2022\1 Jan\3 Emos React` is now `X:\Videos\2022\1 Jan\3 Emos React`
- I keep the same naming schema; we do NOT change the names of folders or files, this makes it easy to find missing assets by simply looking at the missing asset path and checking the other drives for their version of that exact location
- I do not have all my footage or assets stored on `X:\` because it would be too bloated; generally when I am done with footage I store most of it on external drives.
- Assets formerly were stored at; `C:\Users\only1\Videos\Video Editing Assets`
- Assets are currently stored at; `X:\Videos\Video Editing Assets`
- Some of the old assets I no longer use, and have offloaded to external drives - useful if we revisit an old project
- If a project ever complains about missing assets, you MUST check all drives for their variation of that folder
EXAMPLE: The `X:\` drive no longer contains a Video Editing Assets folder starting with "7". I currently have a `D:\` drive plugged in, and there happens to be a `D:\Videos\Video Editing Assets\7 - Music` folder with several gigabytes of old music I no longer use.
- Do **NOT** move the folder onto the `X:\` drive, but rather, use the `D:\Videos\Video Editing Assets\7 - Music` and it's subfolders as the new location of the exact media which is missing.
- Do **NOT** create a new folder beginning with `7` in order to fill the gap - the folder is missing for legacy reasons and the system cannot be altered at this point